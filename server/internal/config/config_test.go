package config

import "testing"

func TestLoadDefaultsListenWildcardButAdvertisesLoopback(t *testing.T) {
	for _, name := range []string{"OMC_SERVER_HOST", "OMC_SERVER_PORT", "OMC_PUBLIC_ORIGIN", "OMC_COOKIE_SECURE", "OMC_TMDB_READ_ACCESS_TOKEN", "OMC_TMDB_API_KEY"} {
		t.Setenv(name, "")
	}
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "0.0.0.0" || config.Port != 3000 || config.PublicOrigin != "http://127.0.0.1:3000" || config.Address() != "0.0.0.0:3000" {
		t.Fatalf("listen and advertised origins were not separated: %+v", config)
	}
}

func TestLoadRejectsWildcardPublicOrigin(t *testing.T) {
	for _, origin := range []string{"http://0.0.0.0:3000", "http://0.0.0.0.:3000", "http://[::]:3000"} {
		t.Run(origin, func(t *testing.T) {
			for _, name := range []string{"OMC_SERVER_HOST", "OMC_SERVER_PORT", "OMC_PUBLIC_ORIGIN", "OMC_COOKIE_SECURE", "OMC_TMDB_READ_ACCESS_TOKEN", "OMC_TMDB_API_KEY"} {
				t.Setenv(name, "")
			}
			t.Setenv("OMC_PUBLIC_ORIGIN", origin)
			if _, err := Load(); err == nil {
				t.Fatal("expected wildcard public origin to be rejected")
			}
		})
	}
}

func TestLoadReadsDeploymentTMDBTokenFromDedicatedRuntimeVariable(t *testing.T) {
	t.Setenv("OMC_SERVER_PORT", "3000")
	t.Setenv("OMC_PUBLIC_ORIGIN", "http://127.0.0.1:3000")
	t.Setenv("OMC_COOKIE_SECURE", "false")
	t.Setenv("OMC_TMDB_READ_ACCESS_TOKEN", "deployment-token")
	t.Setenv("OMC_TMDB_API_KEY", "")
	t.Setenv("OHMYCINE_TMDB_READ_ACCESS_TOKEN", "build-only-token")
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.TMDBDeploymentCredentialValue != "deployment-token" || config.TMDBDeploymentCredentialKind != "read_access_token" {
		t.Fatalf("deployment token source was not isolated: %+v", config)
	}
}

func TestLoadNeverTreatsBuildOnlyTMDBTokenAsRuntimeDeploymentConfig(t *testing.T) {
	t.Setenv("OMC_SERVER_PORT", "3000")
	t.Setenv("OMC_PUBLIC_ORIGIN", "http://127.0.0.1:3000")
	t.Setenv("OMC_COOKIE_SECURE", "false")
	t.Setenv("OMC_TMDB_READ_ACCESS_TOKEN", "")
	t.Setenv("OMC_TMDB_API_KEY", "")
	t.Setenv("OHMYCINE_TMDB_READ_ACCESS_TOKEN", "build-only-token")
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.TMDBDeploymentCredentialValue != "" || config.TMDBDeploymentCredentialKind != "" {
		t.Fatal("build-only TMDB token entered runtime deployment configuration")
	}
}

func TestLoadReadsDeploymentTMDBAPIKeyWithExplicitKind(t *testing.T) {
	t.Setenv("OMC_SERVER_PORT", "3000")
	t.Setenv("OMC_PUBLIC_ORIGIN", "http://127.0.0.1:3000")
	t.Setenv("OMC_COOKIE_SECURE", "false")
	t.Setenv("OMC_TMDB_READ_ACCESS_TOKEN", "")
	t.Setenv("OMC_TMDB_API_KEY", "deployment-api-key")
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.TMDBDeploymentCredentialValue != "deployment-api-key" || config.TMDBDeploymentCredentialKind != "api_key" {
		t.Fatalf("deployment API key source was not explicit: %+v", config)
	}
}

func TestLoadRejectsAmbiguousDeploymentTMDBCredentials(t *testing.T) {
	t.Setenv("OMC_SERVER_PORT", "3000")
	t.Setenv("OMC_PUBLIC_ORIGIN", "http://127.0.0.1:3000")
	t.Setenv("OMC_COOKIE_SECURE", "false")
	t.Setenv("OMC_TMDB_READ_ACCESS_TOKEN", "token")
	t.Setenv("OMC_TMDB_API_KEY", "key")
	if _, err := Load(); err == nil {
		t.Fatal("ambiguous deployment credentials accepted")
	}
}
