package pan115

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

func TestParseCookieNormalizesAllowlistAndDropsOtherFields(t *testing.T) {
	cookie, err := ParseCookie("foo=secret; seid=three=parts; UID=one; CID=two; KID=four")
	if err != nil {
		t.Fatal(err)
	}
	if got := cookie.String(); got != "UID=one; CID=two; SEID=three=parts; KID=four" {
		t.Fatalf("unexpected normalized cookie %q", got)
	}
}

func TestParseCookieRejectsMissingDuplicateOrControlValues(t *testing.T) {
	for _, raw := range []string{
		"UID=1; CID=2",
		"UID=1; CID=2; SEID=3; uid=4",
		"UID=1; CID=2; SEID=3\nCookie: stolen",
		"UID=; CID=2; SEID=3",
	} {
		if _, err := ParseCookie(raw); err == nil {
			t.Fatalf("expected invalid cookie for %q", raw)
		}
	}
}

func TestRiskResponsesBackOffAndOpenConnectionCircuit(t *testing.T) {
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	client := &Client{now: func() time.Time { return now }, jitter: func() time.Duration { return 0 }}
	client.recordOutcome(errors.New("HTTP 405 risk control"))
	if got := client.backoffTil.Sub(now); got != 2*time.Second || client.riskFails != 1 {
		t.Fatalf("first backoff=%s failures=%d", got, client.riskFails)
	}
	client.recordOutcome(errors.New("HTTP 429 too many requests"))
	client.recordOutcome(errors.New("请求频繁"))
	if got := client.backoffTil.Sub(now); got != 8*time.Second || client.circuitTil.Sub(now) != 5*time.Minute {
		t.Fatalf("backoff=%s circuit=%s", got, client.circuitTil.Sub(now))
	}
	if err := client.waitForRecovery(context.Background()); !errors.Is(err, errCircuitOpen) {
		t.Fatalf("circuit error=%v", err)
	}
	if code, retryable := cloud.ErrorInfo(mapError(errors.New("HTTP 405"))); code != cloud.CodeRateLimited || !retryable {
		t.Fatalf("mapped code=%q retryable=%v", code, retryable)
	}
	client.recordOutcome(nil)
	if client.riskFails != 3 || client.backoffTil.IsZero() || client.circuitTil.IsZero() {
		t.Fatal("late success from an in-flight endpoint cleared active risk recovery")
	}
	now = now.Add(6 * time.Minute)
	client.recordOutcome(nil)
	if client.riskFails != 0 || !client.backoffTil.IsZero() || !client.circuitTil.IsZero() {
		t.Fatalf("successful request did not close circuit")
	}
}
