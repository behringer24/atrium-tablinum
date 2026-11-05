package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/behringer24/atrium-tablinum/internal/allowlist"
	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/chunk"
	"github.com/behringer24/atrium-tablinum/internal/config"
	"github.com/behringer24/atrium-tablinum/internal/db"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/embed"
	"github.com/behringer24/atrium-tablinum/internal/httpx"
	"github.com/behringer24/atrium-tablinum/internal/idp"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/jobs"
	mcpserver "github.com/behringer24/atrium-tablinum/internal/mcp"
	"github.com/behringer24/atrium-tablinum/internal/oauth"
	"github.com/behringer24/atrium-tablinum/internal/quota"
	"github.com/behringer24/atrium-tablinum/internal/search"
	"github.com/behringer24/atrium-tablinum/internal/skills"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config.yaml")
	flag.Parse()

	level := new(slog.LevelVar)
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	if err := run(*cfgPath, log, level); err != nil {
		log.Error("Startup failed", "error", err)
		os.Exit(1)
	}
}

func run(cfgPath string, log *slog.Logger, level *slog.LevelVar) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if cfg.Instance.LogLevel != "" {
		_ = level.UnmarshalText([]byte(cfg.Instance.LogLevel))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.Database.URL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, db.Embedding{Model: cfg.Embedding.Model, Dimensions: cfg.Embedding.Dimensions}); err != nil {
		return err
	}

	emb, err := embed.New(cfg.Embedding.Settings())
	if err != nil {
		return err
	}
	log.Info("Embedding backend", "backend", cfg.Embedding.Backend, "model", cfg.Embedding.Model, "dimensions", cfg.Embedding.Dimensions)

	userStore := users.New(pool, users.Options{BootstrapAdmins: cfg.Auth.BootstrapAdmins, PersonalEnabled: cfg.Spaces.Personal.Enabled, LinkByEmail: cfg.Auth.LinkAccountsByEmail})
	spaceStore := spaces.New(pool)
	checker := authz.NewChecker(pool, authz.DefaultCacheTTL)
	userStore.OnChange = checker.InvalidateAll
	spaceStore.OnChange = checker.InvalidateAll

	ing, err := ingest.New(ingest.Config{
		MaxDocumentBytes: cfg.Policy.MaxDocumentBytes,
		Chunk:            chunk.Options{TargetTokens: cfg.Chunking.TargetTokens, MaxTokens: cfg.Chunking.MaxTokens},
		FTSLanguages:     cfg.Search.FTSLanguages,
	}, emb)
	if err != nil {
		return err
	}
	// The limits come from config.yaml; an instance admin overrides them at runtime (admin_quotas)
	// and the override applies to the next write, without a restart.
	quotaStore := quota.New(pool, quota.Defaults{
		Personal: quota.Limits{Documents: cfg.Spaces.Personal.MaxDocuments, Bytes: cfg.Spaces.Personal.MaxBytes,
			Skills: cfg.Skills.PersonalMaxSkills, SkillBytes: cfg.Skills.PersonalMaxBytes, SkillVersions: cfg.Skills.MaxVersionsPerSkill},
		Team: quota.Limits{Documents: cfg.Spaces.Team.MaxDocuments, Bytes: cfg.Spaces.Team.MaxBytes,
			Skills: cfg.Skills.TeamMaxSkills, SkillBytes: cfg.Skills.TeamMaxBytes, SkillVersions: cfg.Skills.MaxVersionsPerSkill},
	})
	docStore := docs.New(pool, spaceStore, ing, docs.Options{
		Quota:              quotaStore,
		HandoffTTLDays:     cfg.Spaces.HandoffTTLDays,
		DuplicateThreshold: cfg.Policy.DuplicateThreshold,
	})
	skillStore := skills.New(pool, spaceStore, skills.Options{AllowSelfApproval: cfg.Skills.AllowSelfApproval, Quota: quotaStore})
	searchSvc, err := search.New(pool, emb, search.Config{
		FTSLanguages: cfg.Search.FTSLanguages, CandidatesPerMethod: cfg.Search.CandidatesPerMethod, RRFK: cfg.Search.RRFK,
		DefaultLimit: cfg.Search.DefaultLimit, MaxLimit: cfg.Search.MaxLimit, HNSWEfSearch: cfg.Search.HNSWEfSearch,
	})
	if err != nil {
		return err
	}

	runner := jobs.New(pool, 5*time.Second)
	runner.AddPeriodic(jobs.Periodic{Name: "expire_handoffs", Every: time.Hour, Run: func(ctx context.Context) error {
		n, err := docStore.ExpireHandoffs(ctx)
		if n > 0 {
			log.Info("Expired handoffs deleted", "count", n)
		}
		return err
	}})
	if days := cfg.Spaces.Personal.RetentionDaysAfterOffboarding; days > 0 {
		runner.AddPeriodic(jobs.Periodic{Name: "purge_offboarded", Every: time.Hour, Run: func(ctx context.Context) error {
			n, err := docStore.PurgeOffboarded(ctx, days)
			if n > 0 {
				log.Info("Personal spaces of blocked accounts deleted after the grace period", "count", n)
			}
			return err
		}})
	}

	icon, err := mcpserver.LoadIcon(cfg.Instance.IconFile)
	if err != nil {
		return err
	}
	mcpSrv := mcpserver.New(
		mcpserver.Deps{
			Users: userStore, Spaces: spaceStore, Docs: docStore, Skills: skillStore, Quota: quotaStore, Search: searchSvc, Checker: checker,
			Pool: pool, Jobs: runner, EmbeddingModel: cfg.Embedding.Model, EmbeddingDimensions: cfg.Embedding.Dimensions,
			Health: []mcpserver.HealthCheck{
				{Name: "database", Check: func(ctx context.Context) (string, error) { return "", pool.Ping(ctx) }},
				{Name: "embedder", Check: func(ctx context.Context) (string, error) { return cfg.Embedding.Backend, emb.Ping(ctx) }},
			},
		},
		mcpserver.Settings{
			ProductName: cfg.Instance.ProductName, ShortName: cfg.Instance.ShortName, OrganizationName: cfg.Instance.OrganizationName,
			IconURL: strings.TrimRight(cfg.Instance.BaseURL, "/") + "/icon", IconMIME: icon.MIME,
			HandoffTTLDays: cfg.Spaces.HandoffTTLDays, DescriptionOverrides: cfg.Tools.Descriptions,
			DefaultSpaceByGroup: cfg.Spaces.DefaultSpaceByGr, WritesPerMinute: cfg.Policy.RateLimitWritesPerMinute, ReadsPerMinute: cfg.Policy.RateLimitReadsPerMinute,
			PersonalRetentionDays: cfg.Spaces.Personal.RetentionDaysAfterOffboarding,
			// A reverse proxy on the same host reaches us via loopback with the public Host header.
			DisableHostProtection: len(cfg.Instance.TrustedProxies) > 0,
		})

	proxies, err := httpx.ParseProxies(cfg.Instance.TrustedProxies)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()

	var verifier auth.TokenVerifier
	resourceMetadataURL := ""
	allowStore := allowlist.New(pool)
	mcpSrv.Allowlist = allowStore
	for _, pc := range cfg.Auth.Providers {
		mcpSrv.LoginProviders = append(mcpSrv.LoginProviders, mcpserver.LoginProvider{
			ID: pc.ID, Type: pc.Type, Domains: pc.AllowedDomains, Emails: pc.AllowedEmails, Tenants: pc.AllowedTenants})
	}
	if len(cfg.Auth.Providers) > 0 {
		providers := make([]idp.Provider, 0, len(cfg.Auth.Providers))
		for _, pc := range cfg.Auth.Providers {
			p, err := idp.New(pc)
			if err != nil {
				return err
			}
			if a, ok := p.(idp.AdditionsSetter); ok {
				a.SetAdditions(allowStore)
			}
			providers = append(providers, p)
		}
		oauthSrv := oauth.NewServer(oauth.Config{
			BaseURL: cfg.Instance.BaseURL, ProductName: cfg.Instance.ProductName,
			AllowedRedirect: cfg.Auth.AllowedRedirectURIs, AllowLoopback: cfg.Auth.AllowLoopbackRedirects, TrustedProxies: proxies,
		}, oauth.NewStore(pool, cfg.Auth.AccessTokenTTL, cfg.Auth.RefreshTokenTTL).WithSessionMaxAge(cfg.Auth.SessionMaxAge), userStore, providers)
		oauthSrv.Register(mux)
		runner.AddPeriodic(jobs.Periodic{Name: "oauth_cleanup", Every: time.Hour, Run: oauthSrv.Cleanup})
		mcpSrv.RevokeTokens = oauthSrv.RevokeUser // offboarding
		mcpSrv.ListSessions = func(ctx context.Context, user uuid.UUID) ([]mcpserver.SessionInfo, error) {
			list, err := oauthSrv.Sessions(ctx, user)
			out := make([]mcpserver.SessionInfo, 0, len(list))
			for _, x := range list {
				out = append(out, mcpserver.SessionInfo{ClientID: x.ClientID, Client: x.ClientName, Started: x.Started, LastUsed: x.LastUsed})
			}
			return out, err
		}
		mcpSrv.RevokeClient = oauthSrv.RevokeClient
		verifier = oauthSrv.Verifier()
		resourceMetadataURL = oauthSrv.ResourceMetadataURL()
	}
	if cfg.Instance.DevMode {
		log.Warn("dev_mode is active - not suitable for production", "user", cfg.Instance.DevUser)
		verifier = devFirst(mcpserver.DevVerifier(userStore, cfg.Instance.DevToken, cfg.Instance.DevUser), verifier)
	}

	mux.Handle("/mcp", mcpSrv.Handler(verifier, resourceMetadataURL))
	mux.Handle("GET /icon", icon.Handler())
	mux.Handle("GET /favicon.ico", icon.Handler())
	mux.Handle("GET /skills/export/{space}/{name}", mcpSrv.SkillExportHandler(verifier, resourceMetadataURL))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("GET /readyz", readiness(log, func(ctx context.Context) error {
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("database unreachable: %w", err)
		}
		if err := emb.Ping(ctx); err != nil {
			return fmt.Errorf("embedder unreachable: %w", err)
		}
		return nil
	}))

	if err := runner.Start(ctx); err != nil {
		return err
	}
	defer runner.Wait()

	// The read timeouts bound only the request (headers and body), so open streams (SSE) are not cut,
	// but a client that sends a body byte by byte cannot hold a connection forever.
	srv := &http.Server{Addr: cfg.Instance.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: time.Minute, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("Server starting", "product", cfg.Instance.ProductName, "listen", cfg.Instance.Listen, "base_url", cfg.Instance.BaseURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// devFirst accepts the static dev token and otherwise defers to the OAuth verifier (if any).
func devFirst(dev, next auth.TokenVerifier) auth.TokenVerifier {
	return func(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
		info, err := dev(ctx, token, r)
		if err == nil || next == nil || !errors.Is(err, auth.ErrInvalidToken) {
			return info, err
		}
		return next(ctx, token, r)
	}
}

// readiness answers /readyz. The endpoint is public, so it says only ok or not ready (what is
// wrong goes to the log, and admin_health shows it to admins), and a result is reused for a few
// seconds so that polling it cannot be used to hammer the database and the embedding service.
func readiness(log *slog.Logger, check func(context.Context) error) http.Handler {
	var (
		mu      sync.Mutex
		checked time.Time
		ready   bool
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if time.Since(checked) > 5*time.Second {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
			err := check(ctx)
			cancel()
			if err != nil && (ready || checked.IsZero()) {
				log.Warn("Not ready", "reason", err)
			}
			ready, checked = err == nil, time.Now()
		}
		ok := ready
		mu.Unlock()
		if !ok {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
}
