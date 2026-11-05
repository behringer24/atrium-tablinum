// Command atrium-license creates the issuer keys and issues and checks Atrium licence files
// (docs/design/licensing.md, docs/licensing.md).
//
// The tool is public on purpose: the secret is the private keys, never the procedure, and a licence
// only means something to a binary that trusts the public key that signed it. What must never be in a
// repository: the private keys it writes, the register of issued licences, and real licences.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/license"
	"github.com/behringer24/atrium-tablinum/internal/product"
)

const keyHeader = "atrium-private-key-v1"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "genesis":
		err = genesis(os.Args[2:])
	case "issue":
		err = issue(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `atrium-license: issuer keys and licence files for Atrium

  atrium-license genesis -out DIR [-count 5]
      Creates the issuer key pairs, OFFLINE, on a machine that is not on a network, into a directory
      on removable media that is not in any repository. Writes one private key file per pair and
      anchors.txt with the public keys, which go into internal/license/anchors.txt.

  atrium-license issue -key FILE -licensee NAME -domain HOST (-months N | -until DATE | -lifetime)
                       [-edition pro] [-features a,b] [-limit users=50]... [-expires DATE] [-register FILE]
      Signs a licence and prints it. -domain is the host of the customer's instance.base_url.
      Dates are YYYY-MM-DD. -months counts from today; -lifetime means maintenance until 2099-12-31.

  atrium-license verify (-file FILE | TOKEN) [-anchors FILE] [-base-url URL]
      Checks the signature against the embedded public keys (or an anchors file) and prints the licence;
      with -base-url it also evaluates it for that instance and this source tree's release line.
`)
}

// ---- genesis ----

func genesis(args []string) error {
	fs := flag.NewFlagSet("genesis", flag.ContinueOnError)
	out := fs.String("out", "", "directory for the key files (offline media, outside any repository)")
	count := fs.Int("count", 5, "number of key pairs (only one signs, the others are cold spares)")
	force := fs.Bool("force", false, "allow a directory inside a git working tree (do not)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || *count < 1 {
		return fmt.Errorf("genesis needs -out DIR and a positive -count")
	}
	if !*force && insideGitTree(*out) {
		return fmt.Errorf("%s is inside a git working tree: private keys must never be in a repository", *out)
	}
	// Never mix a second set into an existing one: it would overwrite anchors.txt.
	if old, _ := filepath.Glob(filepath.Join(*out, "k*.key")); len(old) > 0 {
		return fmt.Errorf("%s already holds keys; genesis writes a new set into an empty directory only", *out)
	}
	if _, err := os.Stat(filepath.Join(*out, "anchors.txt")); err == nil {
		return fmt.Errorf("%s already holds an anchors.txt; genesis writes a new set into an empty directory only", *out)
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	var anchors strings.Builder
	anchors.WriteString("# Public keys: paste these lines into internal/license/anchors.txt. They are not secret.\n")
	for i := 0; i < *count; i++ {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(pub)
		id := "k" + hex.EncodeToString(sum[:])[:8]
		body := fmt.Sprintf("%s\nid %s\nseed %s\n", keyHeader, id, base64.StdEncoding.EncodeToString(priv.Seed()))
		f, err := os.OpenFile(filepath.Join(*out, id+".key"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := f.WriteString(body); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Fprintf(&anchors, "%s %s\n", id, base64.StdEncoding.EncodeToString(pub))
	}
	if err := os.WriteFile(filepath.Join(*out, "anchors.txt"), []byte(anchors.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("%d key pairs written to %s\n\n", *count, *out)
	fmt.Print(anchors.String())
	fmt.Print(`
Next:
  1. Copy the lines above into internal/license/anchors.txt and rebuild.
  2. Move the *.key files to offline media, keep copies in more than one physical place, and delete
     them from this machine. They are the one thing in this system that matters.
  3. Sign with ONE key (issue -key ...); the others stay unused spares.
`)
	return nil
}

// insideGitTree reports whether a path lies inside a git working tree (a .git above it).
func insideGitTree(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}

// ---- issue ----

type limitFlags map[string]int64

func (l limitFlags) String() string { return fmt.Sprint(map[string]int64(l)) }
func (l limitFlags) Set(v string) error {
	name, num, ok := strings.Cut(v, "=")
	n, err := strconv.ParseInt(num, 10, 64)
	if !ok || name == "" || err != nil || n < 0 {
		return fmt.Errorf("-limit takes name=number, e.g. users=50 (0 = unlimited)")
	}
	l[name] = n
	return nil
}

func readKey(path string) (string, ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var id, seed string
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != keyHeader {
		return "", nil, fmt.Errorf("%s is not an Atrium private key file", path)
	}
	for _, l := range lines[1:] {
		k, v, _ := strings.Cut(strings.TrimSpace(l), " ")
		switch k {
		case "id":
			id = v
		case "seed":
			seed = v
		}
	}
	raw, err := base64.StdEncoding.DecodeString(seed)
	if err != nil || len(raw) != ed25519.SeedSize || id == "" {
		return "", nil, fmt.Errorf("%s: unreadable key", path)
	}
	return id, ed25519.NewKeyFromSeed(raw), nil
}

func issue(args []string) error {
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	keyPath := fs.String("key", "", "private key file made by genesis")
	licensee := fs.String("licensee", "", "name of the customer")
	domain := fs.String("domain", "", "host of the customer's instance.base_url")
	edition := fs.String("edition", "pro", "name of the offer, e.g. pro or education (switches nothing; -features and -limit do)")
	features := fs.String("features", "", "comma separated feature names")
	months := fs.Int("months", 0, "maintenance for this many months from today")
	until := fs.String("until", "", "maintenance until this date (YYYY-MM-DD)")
	lifetime := fs.Bool("lifetime", false, "maintenance until 2099-12-31")
	expires := fs.String("expires", "", "optional: the licence has no effect after this date (trials, subscriptions)")
	register := fs.String("register", "", "append the issued licence to this register file (keep it outside the repository)")
	limits := limitFlags{}
	fs.Var(limits, "limit", "name=number, repeatable (0 = unlimited)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyPath == "" || *licensee == "" || *domain == "" {
		return fmt.Errorf("issue needs -key, -licensee and -domain")
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	var maintenance time.Time
	switch {
	case *lifetime:
		maintenance = time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC)
	case *until != "":
		d, err := time.Parse("2006-01-02", *until)
		if err != nil {
			return fmt.Errorf("-until: %w", err)
		}
		maintenance = d
	case *months > 0:
		maintenance = today.AddDate(0, *months, 0)
	default:
		return fmt.Errorf("give the maintenance: -months N, -until DATE or -lifetime")
	}
	keyID, key, err := readKey(*keyPath)
	if err != nil {
		return err
	}
	c := license.Claims{
		V: 1, ID: uuid.NewString(), Licensee: *licensee, Domain: strings.ToLower(strings.TrimSpace(*domain)), Edition: *edition,
		Issued: today.Format("2006-01-02"), MaintenanceUntil: maintenance.Format("2006-01-02"), Expires: *expires, IssuerKey: keyID,
	}
	for _, f := range strings.Split(*features, ",") {
		if f = strings.TrimSpace(f); f != "" {
			c.Features = append(c.Features, f)
		}
	}
	if len(limits) > 0 {
		c.Limits = limits
	}
	token, err := license.Sign(key, c)
	if err != nil {
		return err
	}
	if *register != "" {
		line, _ := json.Marshal(map[string]any{"claims": c, "token": token, "recorded": time.Now().UTC().Format(time.RFC3339)})
		f, err := os.OpenFile(*register, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, werr := f.Write(append(line, '\n'))
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
	}
	fmt.Fprintf(os.Stderr, "licence %s for %s (%s), maintenance until %s, signed with %s\n", c.ID, c.Licensee, c.Domain, c.MaintenanceUntil, keyID)
	fmt.Println(token)
	return nil
}

// ---- verify ----

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	file := fs.String("file", "", "file containing the licence")
	anchorsFile := fs.String("anchors", "", "anchors file instead of the embedded public keys")
	baseURL := fs.String("base-url", "", "also evaluate for this instance.base_url and this source tree's release line")
	if err := fs.Parse(args); err != nil {
		return err
	}
	token := strings.Join(fs.Args(), "")
	if *file != "" {
		b, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		token = string(b)
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("give a licence: TOKEN or -file FILE")
	}
	var anchors license.Anchors
	var err error
	if *anchorsFile != "" {
		b, rerr := os.ReadFile(*anchorsFile)
		if rerr != nil {
			return rerr
		}
		anchors, err = license.ParseAnchors(string(b))
	} else {
		anchors, err = license.EmbeddedAnchors()
	}
	if err != nil {
		return err
	}
	c, err := license.Verify(token, anchors)
	if err != nil {
		return err
	}
	pretty, _ := json.MarshalIndent(c, "", "  ")
	fmt.Printf("signature OK (key %s)\n%s\n", c.IssuerKey, pretty)
	if *baseURL != "" {
		line := product.CurrentLine()
		st := license.Evaluate(&c, line, *baseURL, time.Now())
		fmt.Printf("\nfor %s and line %s (%s): %s. %s\n", *baseURL, line.Name, line.Coverage().Format("2006-01-02"), st.Status, st.Detail)
	}
	return nil
}
