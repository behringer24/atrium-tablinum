package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/smithy-go"
)

func init() { register("bedrock", newBedrock) }

// invoker is the part of the Bedrock runtime client this backend uses (tests replace it).
type invoker interface {
	InvokeModel(ctx context.Context, in *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error)
}

type bedrockFamily int

const (
	titanV2  bedrockFamily = iota // amazon.titan-embed-text-v2:0, one text per request
	cohereV3                      // cohere.embed-multilingual-v3, cohere.embed-english-v3
	cohereV4                      // cohere.embed-v4:0
)

// familyOf maps a model ID (also a cross-region inference profile like eu.cohere...) to
// the request format it needs, and the output dimensions it can produce.
func familyOf(model string) (bedrockFamily, []int, bool) {
	m := model
	for _, p := range []string{"eu.", "us.", "apac.", "global."} {
		m = strings.TrimPrefix(m, p)
	}
	switch {
	case strings.HasPrefix(m, "amazon.titan-embed-text-v2"):
		return titanV2, []int{256, 512, 1024}, true
	case strings.HasPrefix(m, "cohere.embed-v4"):
		return cohereV4, []int{256, 512, 1024, 1536}, true
	case strings.HasPrefix(m, "cohere.embed-"):
		return cohereV3, []int{1024}, true
	}
	return 0, nil, false
}

// bedrock embeds through Amazon Bedrock. Credentials come from the AWS default chain
// (IAM role of the container or instance, environment, profile); none are configured here.
// Options: region (default: AWS_REGION), endpoint (e.g. a VPC endpoint), batch_size (32,
// Cohere only, at most 96), concurrency (4, Titan sends one text per request).
type bedrock struct {
	model    string
	family   bedrockFamily
	dim      int
	region   string
	endpoint string
	batch    int
	conc     int
	timeout  time.Duration

	once    sync.Once
	inv     invoker
	initErr error
	pinged  cachedPing
}

func newBedrock(s Settings, o *options, _ *http.Client) (backend, error) {
	b := &bedrock{
		model:    s.Model,
		dim:      s.Dimensions,
		region:   o.str("region", firstEnv("AWS_REGION", "AWS_DEFAULT_REGION")),
		endpoint: o.str("endpoint", ""),
		batch:    o.integer("batch_size", 32, 1),
		conc:     o.integer("concurrency", 4, 1),
		timeout:  s.Timeout,
	}
	if b.timeout <= 0 {
		b.timeout = defaultTimeout
	}
	fam, dims, ok := familyOf(s.Model)
	switch {
	case s.Model == "":
		o.fail("embedding.model fehlt (z. B. amazon.titan-embed-text-v2:0)")
	case !ok:
		o.fail("modell %q wird nicht unterstützt (amazon.titan-embed-text-v2:0, cohere.embed-multilingual-v3, cohere.embed-english-v3, cohere.embed-v4:0)", s.Model)
	default:
		b.family = fam
		if !contains(dims, s.Dimensions) {
			o.fail("embedding.dimensions %d passt nicht zu %s (möglich: %v)", s.Dimensions, s.Model, dims)
		}
		if fam != titanV2 && b.batch > 96 {
			o.fail("options.batch_size: Cohere erlaubt höchstens 96 Texte je Anfrage")
		}
	}
	if b.region == "" {
		o.fail("options.region fehlt (oder die Umgebungsvariable AWS_REGION setzen)")
	}
	if err := o.done(); err != nil {
		return nil, err
	}
	return b, nil
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func contains(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

// client creates the AWS client on first use, so that a missing credential shows up as a
// readiness error rather than preventing the server from starting.
func (b *bedrock) client() (invoker, error) {
	b.once.Do(func() {
		if b.inv != nil {
			return
		}
		cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(b.region))
		if err != nil {
			b.initErr = fmt.Errorf("aws-konfiguration: %w", err)
			return
		}
		b.inv = bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
			if b.endpoint != "" {
				o.BaseEndpoint = aws.String(b.endpoint)
			}
		})
	})
	return b.inv, b.initErr
}

func (b *bedrock) ping(ctx context.Context) error {
	return b.pinged.check(ctx, func(ctx context.Context) error {
		vecs, err := b.embed(ctx, []string{"ping"}, forDocument)
		if err != nil {
			return err
		}
		if len(vecs) != 1 || len(vecs[0]) != b.dim {
			return fmt.Errorf("embedder liefert andere Dimensionen als konfiguriert (%d)", b.dim)
		}
		return nil
	})
}

func (b *bedrock) embed(ctx context.Context, texts []string, p purpose) ([][]float32, error) {
	inv, err := b.client()
	if err != nil {
		return nil, err
	}
	if b.family == titanV2 {
		return b.embedTitan(ctx, inv, texts)
	}
	return b.embedCohere(ctx, inv, texts, p)
}

// invoke sends one request and decodes the JSON answer into out.
func (b *bedrock) invoke(ctx context.Context, inv invoker, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	res, err := inv.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(b.model),
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
		Body:        payload,
	})
	if err != nil {
		return bedrockError(ctx, err)
	}
	if err := json.Unmarshal(res.Body, out); err != nil {
		return fmt.Errorf("embedding-antwort lesen: %w", err)
	}
	return nil
}

// bedrockError names the AWS error code but not its message, which can echo the input.
func bedrockError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("bedrock-anfrage: %w", ctx.Err())
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "AccessDeniedException":
			return errors.New("bedrock verweigert den Zugriff (AccessDeniedException): IAM-Rechte und Modellzugriff in dieser Region prüfen")
		case "ThrottlingException":
			return errors.New("bedrock drosselt die Anfragen (ThrottlingException)")
		}
		return fmt.Errorf("bedrock-anfrage fehlgeschlagen (%s)", ae.ErrorCode())
	}
	return fmt.Errorf("bedrock-anfrage: %w", err)
}

func (b *bedrock) embedTitan(ctx context.Context, inv invoker, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		failOnce sync.Once
		first    error
		sem      = make(chan struct{}, b.conc)
	)
	for i, t := range texts {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			var resp struct {
				Embedding []float32 `json:"embedding"`
			}
			req := map[string]any{"inputText": t, "dimensions": b.dim, "normalize": true}
			if err := b.invoke(ctx, inv, req, &resp); err != nil {
				failOnce.Do(func() { first = err; cancel() })
				return
			}
			out[i] = resp.Embedding
		}()
	}
	wg.Wait()
	if first != nil {
		return nil, first
	}
	return out, nil
}

func (b *bedrock) embedCohere(ctx context.Context, inv invoker, texts []string, p purpose) ([][]float32, error) {
	inputType := "search_document"
	if p == forQuery {
		inputType = "search_query"
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += b.batch {
		req := map[string]any{
			"texts":           texts[start:min(start+b.batch, len(texts))],
			"input_type":      inputType,
			"embedding_types": []string{"float"},
		}
		if b.family == cohereV4 {
			req["output_dimension"] = b.dim
		} else {
			req["truncate"] = "END" // v3 accepts at most 512 tokens
		}
		var resp struct {
			Embeddings json.RawMessage `json:"embeddings"`
		}
		if err := b.invoke(ctx, inv, req, &resp); err != nil {
			return nil, err
		}
		vecs, err := parseCohereEmbeddings(resp.Embeddings)
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// parseCohereEmbeddings reads both answer shapes: a list of vectors, or an object with
// the vectors per type ({"float": [...]}), which is what embedding_types asks for.
func parseCohereEmbeddings(raw json.RawMessage) ([][]float32, error) {
	var flat [][]float32
	if err := json.Unmarshal(raw, &flat); err == nil {
		return flat, nil
	}
	var byType struct {
		Float [][]float32 `json:"float"`
	}
	if err := json.Unmarshal(raw, &byType); err != nil || byType.Float == nil {
		return nil, errors.New("embedding-antwort lesen: unerwartetes Format")
	}
	return byType.Float, nil
}
