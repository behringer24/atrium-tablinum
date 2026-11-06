package config

import (
	"net/url"
	"sort"
)

// SupportSummary is the part of the configuration a support request may show: named settings and
// limits. It never carries secrets, addresses of other systems, names of people or texts of the
// organisation. It is a list of chosen fields on purpose, so that a new setting appears here only
// when somebody decides it may.
func (c Config) SupportSummary() map[string]any {
	providers := make([]string, 0, len(c.Auth.Providers))
	for _, p := range c.Auth.Providers {
		providers = append(providers, p.Type)
	}
	host := ""
	if u, err := url.Parse(c.Instance.BaseURL); err == nil {
		host = u.Hostname()
	}
	// Option values of a backend may hold anything; their names say enough for support.
	options := make([]string, 0, len(c.Embedding.Options))
	for k := range c.Embedding.Options {
		options = append(options, k)
	}
	sort.Strings(options)
	return map[string]any{
		"instance": map[string]any{
			"host": host, "log_level": c.Instance.LogLevel, "dev_mode": c.Instance.DevMode,
			"trusted_proxies": len(c.Instance.TrustedProxies), "own_icon": c.Instance.IconFile != "",
		},
		"auth": map[string]any{
			"providers": providers, "bootstrap_admins": len(c.Auth.BootstrapAdmins),
			"access_token_ttl": c.Auth.AccessTokenTTL.String(), "refresh_token_ttl": c.Auth.RefreshTokenTTL.String(),
			"session_max_age": c.Auth.SessionMaxAge.String(), "link_accounts_by_email": c.Auth.LinkAccountsByEmail,
			"allowed_redirect_uris": len(c.Auth.AllowedRedirectURIs), "allow_loopback_redirects": c.Auth.AllowLoopbackRedirects,
		},
		"database": map[string]any{"separate_migration_account": c.Database.MigrationURL != ""},
		"embedding": map[string]any{
			"backend": c.Embedding.Backend, "model": c.Embedding.Model, "dimensions": c.Embedding.Dimensions,
			"timeout": c.Embedding.Timeout.String(), "options": options, "allow_dimension_change": c.Embedding.AllowDimensionChange,
		},
		"reranker": map[string]any{"enabled": c.Reranker.Enabled, "backend": c.Reranker.Backend, "candidates": c.Reranker.Candidates},
		"search": map[string]any{
			"fts_languages": c.Search.FTSLanguages, "candidates_per_method": c.Search.CandidatesPerMethod, "rrf_k": c.Search.RRFK,
			"default_limit": c.Search.DefaultLimit, "max_limit": c.Search.MaxLimit, "hnsw_ef_search": c.Search.HNSWEfSearch,
		},
		"chunking": map[string]any{"target_tokens": c.Chunking.TargetTokens, "max_tokens": c.Chunking.MaxTokens},
		"spaces": map[string]any{
			"personal_enabled": c.Spaces.Personal.Enabled, "personal_max_documents": c.Spaces.Personal.MaxDocuments,
			"personal_max_bytes": c.Spaces.Personal.MaxBytes, "retention_days_after_offboarding": c.Spaces.Personal.RetentionDaysAfterOffboarding,
			"team_max_documents": c.Spaces.Team.MaxDocuments, "team_max_bytes": c.Spaces.Team.MaxBytes,
			"handoff_ttl_days": c.Spaces.HandoffTTLDays, "default_space_by_group": len(c.Spaces.DefaultSpaceByGr),
		},
		"policy": map[string]any{
			"duplicate_threshold": c.Policy.DuplicateThreshold, "max_document_bytes": c.Policy.MaxDocumentBytes,
			"rate_limit_writes_per_minute": c.Policy.RateLimitWritesPerMinute, "rate_limit_reads_per_minute": c.Policy.RateLimitReadsPerMinute,
		},
		"skills": map[string]any{
			"allow_self_approval": c.Skills.AllowSelfApproval, "personal_max_skills": c.Skills.PersonalMaxSkills,
			"personal_max_bytes": c.Skills.PersonalMaxBytes, "team_max_skills": c.Skills.TeamMaxSkills,
			"team_max_bytes": c.Skills.TeamMaxBytes, "max_versions_per_skill": c.Skills.MaxVersionsPerSkill,
		},
		"license": map[string]any{"file": c.License.File != "", "community_max_users": c.License.CommunityMaxUsers},
		"tools":   map[string]any{"description_overrides": len(c.Tools.Descriptions)},
	}
}
