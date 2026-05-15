// Package pool 账号池管理
// 实现轮询负载均衡、错误冷却、Token 刷新
package pool

import (
	"fmt"
	"kiro-go/config"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// AccountPool 账号池
type AccountPool struct {
	mu            sync.RWMutex
	accounts      []config.Account
	currentIndex  uint64
	cooldowns     map[string]time.Time // 账号冷却时间
	errorCounts   map[string]int       // 连续错误计数
	accountModels map[string]map[string]struct{}
}

type AccountAvailability struct {
	ID             string   `json:"id"`
	Available      bool     `json:"available"`
	Reason         string   `json:"reason,omitempty"`
	CooldownUntil  int64    `json:"cooldownUntil,omitempty"`
	ExpiresAt      int64    `json:"expiresAt,omitempty"`
	UsageCurrent   float64  `json:"usageCurrent,omitempty"`
	UsageLimit     float64  `json:"usageLimit,omitempty"`
	SupportedModel []string `json:"supportedModels,omitempty"`
}

var (
	pool               *AccountPool
	poolOnce           sync.Once
	claudeVersionRegex = regexp.MustCompile(`^(claude-(?:opus|sonnet|haiku))-(\d+)[.-](\d+)$`)
)

// GetPool 获取全局账号池单例
func GetPool() *AccountPool {
	poolOnce.Do(func() {
		pool = &AccountPool{
			cooldowns:     make(map[string]time.Time),
			errorCounts:   make(map[string]int),
			accountModels: make(map[string]map[string]struct{}),
		}
		pool.Reload()
	})
	return pool
}

// Reload 从配置重新加载账号
// 构建加权列表：weight<=1 出现 1 次，weight>=2 出现 weight 次
func (p *AccountPool) Reload() {
	p.mu.Lock()
	defer p.mu.Unlock()
	enabled := config.GetEnabledAccounts()
	var weighted []config.Account
	enabledIDs := make(map[string]struct{}, len(enabled))
	for _, a := range enabled {
		enabledIDs[a.ID] = struct{}{}
		w := a.Weight
		if w < 1 {
			w = 1
		}
		for j := 0; j < w; j++ {
			weighted = append(weighted, a)
		}
	}
	p.accounts = weighted
	for id := range p.accountModels {
		if _, ok := enabledIDs[id]; !ok {
			delete(p.accountModels, id)
		}
	}
}

// GetNext 获取下一个可用账号（加权轮询）
func (p *AccountPool) GetNext() *config.Account {
	return p.getNextLocked(nil)
}

// GetNextForModel 获取支持指定模型的下一个可用账号。
func (p *AccountPool) GetNextForModel(model string) *config.Account {
	return p.GetNextForModelExcluding(model, nil)
}

// GetNextForModelExcluding 获取支持指定模型、且不在排除列表中的下一个可用账号。
func (p *AccountPool) GetNextForModelExcluding(model string, excluded map[string]struct{}) *config.Account {
	keys := modelLookupKeys(model)
	return p.getNextLocked(func(acc *config.Account) bool {
		if _, skip := excluded[acc.ID]; skip {
			return false
		}
		if len(keys) == 0 {
			return true
		}
		return p.supportsModelLocked(acc.ID, keys)
	})
}

func (p *AccountPool) GetPreferredForModel(model string, preferredID string, excluded map[string]struct{}) *config.Account {
	preferredID = strings.TrimSpace(preferredID)
	if preferredID == "" {
		return nil
	}

	keys := modelLookupKeys(model)
	return p.getByIDLocked(preferredID, func(acc *config.Account) bool {
		if _, skip := excluded[acc.ID]; skip {
			return false
		}
		if len(keys) == 0 {
			return true
		}
		return p.supportsModelLocked(acc.ID, keys)
	})
}

func (p *AccountPool) getByIDLocked(id string, filter func(*config.Account) bool) *config.Account {
	p.mu.RLock()
	defer p.mu.RUnlock()

	now := time.Now()
	for i := range p.accounts {
		acc := &p.accounts[i]
		if acc.ID != id {
			continue
		}
		if filter != nil && !filter(acc) {
			return nil
		}
		if !p.isRuntimeAvailableLocked(acc, now) {
			return nil
		}
		return acc
	}
	return nil
}

func (p *AccountPool) getNextLocked(filter func(*config.Account) bool) *config.Account {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.accounts) == 0 {
		return nil
	}

	now := time.Now()
	n := len(p.accounts)
	seen := make(map[string]bool)

	// 加权轮询查找可用账号
	for i := 0; i < n; i++ {
		idx := atomic.AddUint64(&p.currentIndex, 1) % uint64(n)
		acc := &p.accounts[idx]

		if seen[acc.ID] {
			continue
		}
		if filter != nil && !filter(acc) {
			seen[acc.ID] = true
			continue
		}

		if !p.isRuntimeAvailableLocked(acc, now) {
			seen[acc.ID] = true
			continue
		}

		return acc
	}

	// 无可用账号，返回冷却时间最短的（排除额度用尽的）
	var best *config.Account
	var earliest time.Time
	for i := range p.accounts {
		acc := &p.accounts[i]
		if filter != nil && !filter(acc) {
			continue
		}
		// 额度用尽的账号不作为 fallback
		if acc.UsageLimit > 0 && acc.UsageCurrent >= acc.UsageLimit {
			continue
		}
		if cooldown, ok := p.cooldowns[acc.ID]; ok {
			if best == nil || cooldown.Before(earliest) {
				best = acc
				earliest = cooldown
			}
		} else {
			return acc
		}
	}
	return best
}

func (p *AccountPool) isRuntimeAvailableLocked(acc *config.Account, now time.Time) bool {
	if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
		return false
	}
	if acc.ExpiresAt > 0 && now.Unix() > acc.ExpiresAt-300 {
		return false
	}
	if acc.UsageLimit > 0 && acc.UsageCurrent >= acc.UsageLimit {
		return false
	}
	return true
}

func modelLookupKeys(model string) []string {
	raw := strings.ToLower(strings.TrimSpace(model))
	if raw == "" {
		return nil
	}

	seen := map[string]struct{}{raw: {}}
	keys := []string{raw}
	add := func(candidate string) {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		keys = append(keys, candidate)
	}

	if matches := claudeVersionRegex.FindStringSubmatch(raw); len(matches) == 4 {
		add(fmt.Sprintf("%s-%s.%s", matches[1], matches[2], matches[3]))
		add(fmt.Sprintf("%s-%s-%s", matches[1], matches[2], matches[3]))
	}

	return keys
}

func (p *AccountPool) supportsModelLocked(accountID string, modelKeys []string) bool {
	supported, ok := p.accountModels[accountID]
	if !ok || len(supported) == 0 {
		return false
	}
	for _, key := range modelKeys {
		if _, exists := supported[key]; exists {
			return true
		}
	}
	return false
}

// SetAccountModels 更新单个账号的模型支持集合。
func (p *AccountPool) SetAccountModels(id string, models []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(models) == 0 {
		delete(p.accountModels, id)
		return
	}

	supported := make(map[string]struct{}, len(models))
	for _, model := range models {
		for _, key := range modelLookupKeys(model) {
			supported[key] = struct{}{}
		}
	}
	if len(supported) == 0 {
		delete(p.accountModels, id)
		return
	}
	p.accountModels[id] = supported
}

// GetByID 根据 ID 获取账号
func (p *AccountPool) GetByID(id string) *config.Account {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			return &p.accounts[i]
		}
	}
	return nil
}

// RecordSuccess 记录请求成功，清除冷却
func (p *AccountPool) RecordSuccess(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cooldowns, id)
	p.errorCounts[id] = 0
}

// RecordError 记录请求错误，设置冷却
func (p *AccountPool) RecordError(id string, isQuotaError bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.errorCounts[id]++

	if isQuotaError {
		// 配额错误，冷却 1 小时
		p.cooldowns[id] = time.Now().Add(time.Hour)
	} else if p.errorCounts[id] >= 3 {
		// 连续 3 次错误，冷却 1 分钟
		p.cooldowns[id] = time.Now().Add(time.Minute)
	}
}

// ExtendCooldown 延长账号冷却时间；如果已有更长冷却，则保留更长值。
func (p *AccountPool) ExtendCooldown(id string, duration time.Duration) {
	if duration <= 0 {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	until := time.Now().Add(duration)
	if current, ok := p.cooldowns[id]; ok && current.After(until) {
		return
	}
	p.cooldowns[id] = until
}

// SetCurrentIndexForTest overrides the round-robin cursor for deterministic tests.
func (p *AccountPool) SetCurrentIndexForTest(v uint64) {
	atomic.StoreUint64(&p.currentIndex, v)
}

// UpdateToken 更新账号 Token
func (p *AccountPool) UpdateToken(id, accessToken, refreshToken string, expiresAt int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			p.accounts[i].AccessToken = accessToken
			if refreshToken != "" {
				p.accounts[i].RefreshToken = refreshToken
			}
			p.accounts[i].ExpiresAt = expiresAt
			break
		}
	}
}

// Count 返回账号总数
func (p *AccountPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.accounts)
}

// AvailableCount 返回可用账号数
func (p *AccountPool) AvailableCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	now := time.Now()
	count := 0
	for _, acc := range p.accounts {
		if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
			continue
		}
		count++
	}
	return count
}

func (p *AccountPool) AvailabilitySnapshot() []AccountAvailability {
	p.mu.RLock()
	defer p.mu.RUnlock()

	now := time.Now()
	seen := make(map[string]struct{}, len(p.accounts))
	result := make([]AccountAvailability, 0, len(p.accounts))
	for _, acc := range p.accounts {
		if _, ok := seen[acc.ID]; ok {
			continue
		}
		seen[acc.ID] = struct{}{}

		item := AccountAvailability{
			ID:           acc.ID,
			Available:    true,
			ExpiresAt:    acc.ExpiresAt,
			UsageCurrent: acc.UsageCurrent,
			UsageLimit:   acc.UsageLimit,
		}
		if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
			item.Available = false
			item.Reason = "cooldown"
			item.CooldownUntil = cooldown.Unix()
		} else if acc.ExpiresAt > 0 && now.Unix() > acc.ExpiresAt-300 {
			item.Available = false
			item.Reason = "token_expiring"
		} else if acc.UsageLimit > 0 && acc.UsageCurrent >= acc.UsageLimit {
			item.Available = false
			item.Reason = "usage_exhausted"
		}

		if models := p.accountModels[acc.ID]; len(models) > 0 {
			item.SupportedModel = make([]string, 0, len(models))
			for model := range models {
				item.SupportedModel = append(item.SupportedModel, model)
			}
		}

		result = append(result, item)
	}
	return result
}

// UpdateStats 更新账号统计
func (p *AccountPool) UpdateStats(id string, tokens int, credits float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			p.accounts[i].RequestCount++
			p.accounts[i].TotalTokens += tokens
			p.accounts[i].TotalCredits += credits
			p.accounts[i].LastUsed = time.Now().Unix()
			go config.UpdateAccountStats(id, p.accounts[i].RequestCount, p.accounts[i].ErrorCount, p.accounts[i].TotalTokens, p.accounts[i].TotalCredits, p.accounts[i].LastUsed)
			break
		}
	}
}

// GetAllAccounts 获取所有账号副本
func (p *AccountPool) GetAllAccounts() []config.Account {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]config.Account, len(p.accounts))
	copy(result, p.accounts)
	return result
}
