package pool

import (
	"kiro-go/config"
	"testing"
	"time"
)

func TestGetNextForModelSkipsUnsupportedAccounts(t *testing.T) {
	p := &AccountPool{
		accounts: []config.Account{
			{ID: "acct-a"},
			{ID: "acct-b"},
		},
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		accountModels: make(map[string]map[string]struct{}),
	}

	p.SetAccountModels("acct-a", []string{"claude-sonnet-4.5"})
	p.SetAccountModels("acct-b", []string{"claude-opus-4-7"})

	account := p.GetNextForModel("claude-opus-4.7")
	if account == nil {
		t.Fatalf("expected a matching account")
	}
	if account.ID != "acct-b" {
		t.Fatalf("expected acct-b, got %s", account.ID)
	}
}

func TestGetNextForModelMatchesDashAndDotClaudeVariants(t *testing.T) {
	p := &AccountPool{
		accounts: []config.Account{
			{ID: "acct-a"},
		},
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		accountModels: make(map[string]map[string]struct{}),
	}

	p.SetAccountModels("acct-a", []string{"claude-opus-4-7"})

	for _, model := range []string{"claude-opus-4-7", "claude-opus-4.7"} {
		account := p.GetNextForModel(model)
		if account == nil {
			t.Fatalf("expected a matching account for %s", model)
		}
		if account.ID != "acct-a" {
			t.Fatalf("expected acct-a for %s, got %s", model, account.ID)
		}
	}
}

func TestGetNextForModelReturnsNilWithoutModelSupport(t *testing.T) {
	p := &AccountPool{
		accounts: []config.Account{
			{ID: "acct-a"},
		},
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		accountModels: make(map[string]map[string]struct{}),
	}

	p.SetAccountModels("acct-a", []string{"claude-sonnet-4.5"})

	if account := p.GetNextForModel("claude-opus-4.7"); account != nil {
		t.Fatalf("expected no matching account, got %s", account.ID)
	}
}

func TestGetNextForModelExcludingSkipsTriedAccount(t *testing.T) {
	p := &AccountPool{
		accounts: []config.Account{
			{ID: "acct-a"},
			{ID: "acct-b"},
		},
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		accountModels: make(map[string]map[string]struct{}),
	}

	p.SetAccountModels("acct-a", []string{"claude-opus-4.7"})
	p.SetAccountModels("acct-b", []string{"claude-opus-4.7"})

	account := p.GetNextForModelExcluding("claude-opus-4.7", map[string]struct{}{
		"acct-a": {},
	})
	if account == nil {
		t.Fatalf("expected a matching account")
	}
	if account.ID != "acct-b" {
		t.Fatalf("expected acct-b, got %s", account.ID)
	}
}

func TestExtendCooldownKeepsLongerExistingWindow(t *testing.T) {
	p := &AccountPool{
		cooldowns:     make(map[string]time.Time),
		errorCounts:   make(map[string]int),
		accountModels: make(map[string]map[string]struct{}),
	}

	longer := time.Now().Add(3 * time.Minute)
	p.cooldowns["acct-a"] = longer
	p.ExtendCooldown("acct-a", time.Minute)

	got := p.cooldowns["acct-a"]
	if got.Before(longer.Add(-time.Second)) {
		t.Fatalf("expected longer cooldown to remain, got %v want >= %v", got, longer)
	}
}
