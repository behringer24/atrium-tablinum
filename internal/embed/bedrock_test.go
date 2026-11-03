package embed

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/smithy-go"
)

type bedrockCall struct {
	Model string
	Body  map[string]any
}

// fakeBedrock answers like the Bedrock runtime for Titan and Cohere models. The first
// number of every vector is the length of the text, so the order can be checked.
type fakeBedrock struct {
	mu       sync.Mutex
	calls    []bedrockCall
	dim      int
	err      error
	cohere   string // "", "flat" or "typed": the answer shape for Cohere models
	inflight atomic.Int32
	maxSeen  atomic.Int32
}

func (f *fakeBedrock) InvokeModel(_ context.Context, in *bedrockruntime.InvokeModelInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
	n := f.inflight.Add(1)
	defer f.inflight.Add(-1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond)
	var body map[string]any
	_ = json.Unmarshal(in.Body, &body)
	f.mu.Lock()
	f.calls = append(f.calls, bedrockCall{*in.ModelId, body})
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	vec := func(s string) []float32 {
		v := make([]float32, f.dim)
		v[0] = float32(len(s))
		return v
	}
	if text, ok := body["inputText"].(string); ok {
		b, _ := json.Marshal(map[string]any{"embedding": vec(text), "inputTextCount": 1})
		return &bedrockruntime.InvokeModelOutput{Body: b}, nil
	}
	var vecs [][]float32
	for _, t := range body["texts"].([]any) {
		vecs = append(vecs, vec(t.(string)))
	}
	answer := map[string]any{"embeddings": vecs}
	if f.cohere == "typed" {
		answer["embeddings"] = map[string]any{"float": vecs}
	}
	b, _ := json.Marshal(answer)
	return &bedrockruntime.InvokeModelOutput{Body: b}, nil
}

func newBedrockForTest(t *testing.T, model string, dim int, f *fakeBedrock, opts map[string]string) Embedder {
	t.Helper()
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	if opts == nil {
		opts = map[string]string{}
	}
	opts["region"] = "eu-central-1"
	e, err := New(Settings{Backend: "bedrock", Model: model, Dimensions: dim, Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	f.dim = dim
	e.(*wrapped).b.(*bedrock).inv = f
	return e
}

func TestBedrockTitanKeepsOrderAndLimitsConcurrency(t *testing.T) {
	f := &fakeBedrock{}
	e := newBedrockForTest(t, "amazon.titan-embed-text-v2:0", 1024, f, map[string]string{"concurrency": "3"})
	var texts []string
	for i := 1; i <= 20; i++ {
		texts = append(texts, strings.Repeat("x", i))
	}
	vecs, err := e.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range vecs {
		if int(v[0]) != i+1 {
			t.Fatalf("order violated at %d", i)
		}
	}
	if len(f.calls) != 20 || f.maxSeen.Load() > 3 {
		t.Errorf("requests=%d, at most %d concurrent", len(f.calls), f.maxSeen.Load())
	}
	c := f.calls[0]
	if c.Model != "amazon.titan-embed-text-v2:0" || c.Body["dimensions"] != float64(1024) || c.Body["normalize"] != true {
		t.Errorf("request: %+v", c)
	}
}

func TestBedrockCohereInputTypesAndAnswerShapes(t *testing.T) {
	for _, shape := range []string{"flat", "typed"} {
		f := &fakeBedrock{cohere: shape}
		e := newBedrockForTest(t, "cohere.embed-multilingual-v3", 1024, f, map[string]string{"batch_size": "2"})
		vecs, err := e.Embed(context.Background(), []string{"a", "bb", "ccc"})
		if err != nil || len(vecs) != 3 || int(vecs[2][0]) != 3 {
			t.Fatalf("%s: %v %v", shape, vecs, err)
		}
		if _, err := Query(context.Background(), e, "frage"); err != nil {
			t.Fatal(err)
		}
		if len(f.calls) != 3 {
			t.Fatalf("%s: %d requests", shape, len(f.calls))
		}
		if f.calls[0].Body["input_type"] != "search_document" || f.calls[2].Body["input_type"] != "search_query" ||
			f.calls[0].Body["truncate"] != "END" {
			t.Errorf("%s: %+v", shape, f.calls)
		}
	}
}

func TestBedrockCohereV4AndInferenceProfile(t *testing.T) {
	f := &fakeBedrock{cohere: "typed"}
	e := newBedrockForTest(t, "eu.cohere.embed-v4:0", 1536, f, nil)
	if _, err := e.Embed(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	b := f.calls[0].Body
	if f.calls[0].Model != "eu.cohere.embed-v4:0" || b["output_dimension"] != float64(1536) {
		t.Errorf("request: %+v", f.calls[0])
	}
	if _, has := b["truncate"]; has {
		t.Error("v4 gets no truncate")
	}
}

func TestBedrockValidation(t *testing.T) {
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	region := map[string]string{"region": "eu-central-1"}
	for name, tc := range map[string]struct {
		s    Settings
		want string
	}{
		"model missing":          {Settings{Backend: "bedrock", Dimensions: 1024, Options: region}, "embedding.model"},
		"model unknown":          {Settings{Backend: "bedrock", Model: "BAAI/bge-m3", Dimensions: 1024, Options: region}, "not supported"},
		"dimension Titan":        {Settings{Backend: "bedrock", Model: "amazon.titan-embed-text-v2:0", Dimensions: 768, Options: region}, "possible"},
		"dimension Cohere v3":    {Settings{Backend: "bedrock", Model: "cohere.embed-english-v3", Dimensions: 512, Options: region}, "possible"},
		"region missing":         {Settings{Backend: "bedrock", Model: "amazon.titan-embed-text-v2:0", Dimensions: 1024}, "region"},
		"Cohere batch too large": {Settings{Backend: "bedrock", Model: "cohere.embed-v4:0", Dimensions: 1024, Options: map[string]string{"region": "x", "batch_size": "200"}}, "96"},
		"option unknown":         {Settings{Backend: "bedrock", Model: "amazon.titan-embed-text-v2:0", Dimensions: 1024, Options: map[string]string{"region": "x", "key": "y"}}, "unknown options"},
	} {
		if err := Validate(tc.s); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// the region may also come from the environment
	t.Setenv("AWS_REGION", "eu-west-1")
	if err := Validate(Settings{Backend: "bedrock", Model: "amazon.titan-embed-text-v2:0", Dimensions: 1024}); err != nil {
		t.Errorf("region from AWS_REGION: %v", err)
	}
}

func TestBedrockErrorsNameTheCodeButNotTheMessage(t *testing.T) {
	for code, want := range map[string]string{
		"AccessDeniedException": "model access",
		"ThrottlingException":   "throttles",
		"ValidationException":   "ValidationException",
	} {
		f := &fakeBedrock{err: &smithy.GenericAPIError{Code: code, Message: "secret text in the error"}}
		e := newBedrockForTest(t, "amazon.titan-embed-text-v2:0", 1024, f, nil)
		_, err := e.Embed(context.Background(), []string{"x", "y", "z"})
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: %v", code, err)
		}
	}
	f := &fakeBedrock{err: errors.New("no EC2 IMDS role found")}
	e := newBedrockForTest(t, "cohere.embed-v4:0", 1024, f, nil)
	if _, err := e.Embed(context.Background(), []string{"x"}); err == nil || !strings.Contains(err.Error(), "IMDS") {
		t.Errorf("transport error: %v", err)
	}
}

func TestBedrockPingIsCachedAndChecksDimension(t *testing.T) {
	f := &fakeBedrock{}
	e := newBedrockForTest(t, "amazon.titan-embed-text-v2:0", 1024, f, nil)
	for i := 0; i < 3; i++ {
		if err := e.Ping(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.calls) != 1 {
		t.Errorf("ping costs %d requests", len(f.calls))
	}
	bad := &fakeBedrock{}
	e2 := newBedrockForTest(t, "amazon.titan-embed-text-v2:0", 512, bad, nil)
	bad.dim = 256 // the service returns something other than configured
	if err := e2.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Errorf("wrong dimension: %v", err)
	}
}
