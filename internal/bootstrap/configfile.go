package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const configDirName = ".ainovel"
const projectProfilesDir = "configs"

// DefaultConfigPath 返回全局配置文件路径 ~/.ainovel/config.json。
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configDirName, "config.json")
}

// DefaultConfigDir 返回 ~/.ainovel 目录路径；取不到家目录时返回空字符串。
// 仅用于读/写不强制存在的文件（如模型缓存），不会自动创建目录。
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configDirName)
}

// configDir 返回 ~/.ainovel 目录路径，不存在时创建。
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// LoadConfig 按优先级加载并合并配置：
//  1. ~/.ainovel/config.json（全局）
//  2. ./configs/*.json（项目级模型配置档案）
//  3. ./ainovel.json（项目级覆盖）
//  4. flagPath 指定的路径（最高优先级）
func LoadConfig(flagPath string) (Config, error) {
	var cfg Config

	// 1. 全局配置。它是最低优先级基底，坏文件降级为告警而非阻断——可被项目级
	//    / --config 覆盖；硬失败会把"坏全局 + 有效 --config"的用户挡在门外，
	//    违反 --config"我明确指定这个"的语义。
	if p := DefaultConfigPath(); p != "" {
		global, found, err := loadOptionalJSON(p)
		switch {
		case err != nil:
			slog.Warn("全局配置解析失败，已忽略（可被项目级/--config 覆盖）", "module", "config", "path", p, "err", err)
		case found:
			cfg = global
		}
	}

	// 2. 项目级模型配置档案。坏文件 fail loud：这是当前项目主动声明的模型入口。
	profiles, err := LoadConfigProfiles(projectProfilesDir)
	if err != nil {
		return cfg, err
	}
	if len(profiles) > 0 {
		cfg = mergeConfigProfiles(cfg, profiles)
	}

	// 3. 项目级覆盖。坏文件 fail loud：用户在当前目录主动放的配置，静默吞掉会让
	//    "配了不生效"无从排查（issue #37）。
	project, found, err := loadOptionalJSON("ainovel.json")
	if err != nil {
		return cfg, fmt.Errorf("项目级配置 ./ainovel.json 解析失败（请检查 JSON 语法）: %w", err)
	}
	if found {
		cfg = mergeConfig(cfg, project)
	}

	// 4. CLI flag 覆盖
	if flagPath != "" {
		override, err := loadJSONFile(flagPath)
		if err != nil {
			return cfg, fmt.Errorf("load config %s: %w", flagPath, err)
		}
		cfg = mergeConfig(cfg, override)
	}

	return cfg, nil
}

// loadOptionalJSON 读取一个可选的配置文件：
//   - 文件不存在 → (zero, false, nil)，由调用方决定用默认/上层值
//   - 文件存在但解析失败 → 返回错误（不再静默吞掉——否则用户的配置"配了不生效"
//     却无从排查，正是 issue #37 的根因）
func loadOptionalJSON(path string) (Config, bool, error) {
	cfg, err := loadJSONFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}
	return cfg, true, nil
}

// LoadConfigFile 读取单个 JSON 配置文件，支持 // 行注释。
// 不做任何合并，仅返回该文件自身的配置。文件不存在时返回错误。
func LoadConfigFile(path string) (Config, error) {
	return loadJSONFile(path)
}

// loadJSONFile 读取 JSON 配置文件，支持 // 行注释。
// 文件不存在时返回错误（由调用方决定是否忽略）。
func loadJSONFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cleaned := stripJSONComments(data)
	var cfg Config
	if err := json.Unmarshal(cleaned, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// mergeConfig 将 overlay 合并到 base 上。非零值字段覆盖，map 按 key 合并。
func mergeConfig(base, overlay Config) Config {
	if overlay.Provider != "" {
		base.Provider = overlay.Provider
	}
	if overlay.ModelName != "" {
		base.ModelName = overlay.ModelName
	}
	if overlay.Style != "" {
		base.Style = overlay.Style
	}
	if overlay.ContextWindow > 0 {
		base.ContextWindow = overlay.ContextWindow
	}

	// Providers: overlay 的 key 覆盖 base 同名 key
	if len(overlay.Providers) > 0 {
		if base.Providers == nil {
			base.Providers = make(map[string]ProviderConfig)
		}
		for k, v := range overlay.Providers {
			existing := base.Providers[k]
			if v.Type != "" {
				existing.Type = v.Type
			}
			if v.APIKey != "" {
				existing.APIKey = v.APIKey
			}
			if v.BaseURL != "" {
				existing.BaseURL = v.BaseURL
			}
			if len(v.Models) > 0 {
				existing.Models = append([]string(nil), v.Models...)
			}
			if len(v.ExtraBody) > 0 {
				existing.ExtraBody = cloneAnyMap(v.ExtraBody)
			}
			base.Providers[k] = existing
		}
	}

	// Roles: overlay 的 key 覆盖 base 同名 key
	if len(overlay.Roles) > 0 {
		if base.Roles == nil {
			base.Roles = make(map[string]RoleConfig)
		}
		for k, v := range overlay.Roles {
			existing := base.Roles[k]
			if v.Provider != "" {
				existing.Provider = v.Provider
			}
			if v.Model != "" {
				existing.Model = v.Model
			}
			if len(v.Fallbacks) > 0 {
				existing.Fallbacks = append([]ModelRef(nil), v.Fallbacks...)
			}
			base.Roles[k] = existing
		}
	}

	// Budget / Notify：整块覆盖（项目级预算/告警是独立政策声明，不与全局逐字段拼接）
	if overlay.Budget != (BudgetConfig{}) {
		base.Budget = overlay.Budget
	}
	if overlay.Notify.Enabled != nil || overlay.Notify.Command != "" || len(overlay.Notify.Events) > 0 {
		base.Notify = overlay.Notify
	}

	return base
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ConfigProfile 是 ./configs/*.json 中的单个模型连接配置。
// 它保留更直观的单档案写法，同时会转换成 Config.Providers 供现有运行时复用。
type ConfigProfile struct {
	Name           string         `json:"name,omitempty"`
	Provider       string         `json:"provider,omitempty"`
	Type           string         `json:"type,omitempty"`
	Compatibility  string         `json:"compatibility,omitempty"`
	CompatibleMode string         `json:"compatible_mode,omitempty"`
	APIKey         string         `json:"api_key,omitempty"`
	APIKeyCamel    string         `json:"apiKey,omitempty"`
	BaseURL        string         `json:"base_url,omitempty"`
	BaseURLCamel   string         `json:"baseUrl,omitempty"`
	Model          string         `json:"model,omitempty"`
	Models         []string       `json:"models,omitempty"`
	ContextWindow  int            `json:"context_window,omitempty"`
	Default        bool           `json:"default,omitempty"`
	ExtraBody      map[string]any `json:"extra_body,omitempty"`
	Path           string         `json:"-"`
}

func (p ConfigProfile) DisplayName() string {
	if strings.TrimSpace(p.Name) != "" {
		return strings.TrimSpace(p.Name)
	}
	return p.ProviderName()
}

func (p ConfigProfile) ProviderName() string {
	if strings.TrimSpace(p.Provider) != "" {
		return strings.TrimSpace(p.Provider)
	}
	if strings.TrimSpace(p.Name) != "" {
		return slugConfigName(p.Name)
	}
	base := strings.TrimSuffix(filepath.Base(p.Path), filepath.Ext(p.Path))
	if base != "" {
		return slugConfigName(base)
	}
	return ""
}

func (p ConfigProfile) ProviderType() string {
	for _, candidate := range []string{p.Type, p.CompatibleMode, p.Compatibility} {
		if strings.TrimSpace(candidate) != "" {
			return strings.ToLower(strings.TrimSpace(candidate))
		}
	}
	return ""
}

func (p ConfigProfile) Key() string {
	if p.APIKey != "" {
		return p.APIKey
	}
	return p.APIKeyCamel
}

func (p ConfigProfile) URL() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return p.BaseURLCamel
}

func (p ConfigProfile) PrimaryModel() string {
	if strings.TrimSpace(p.Model) != "" {
		return strings.TrimSpace(p.Model)
	}
	if len(p.Models) > 0 {
		return strings.TrimSpace(p.Models[0])
	}
	return ""
}

func (p ConfigProfile) providerConfig() ProviderConfig {
	return ProviderConfig{
		Type:      p.ProviderType(),
		APIKey:    p.Key(),
		BaseURL:   p.URL(),
		Models:    append([]string(nil), p.Models...),
		ExtraBody: cloneAnyMap(p.ExtraBody),
	}
}

func (p ConfigProfile) validate() error {
	provider := p.ProviderName()
	if provider == "" {
		return fmt.Errorf("%s: provider/name is required", p.Path)
	}
	if err := validateConfigText("config profile provider", provider); err != nil {
		return err
	}
	if err := validateProviderConfigText(provider, p.providerConfig()); err != nil {
		return err
	}
	model := p.PrimaryModel()
	if model == "" {
		return fmt.Errorf("%s: model or models[0] is required", p.Path)
	}
	if err := validateConfigText("config profile model", model); err != nil {
		return err
	}
	return nil
}

// LoadConfigProfiles 读取项目目录下的模型配置档案。目录不存在时返回空列表。
func LoadConfigProfiles(dir string) ([]ConfigProfile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取配置目录 %s 失败: %w", dir, err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if isConfigProfileExampleFile(entry.Name()) {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(files)

	profiles := make([]ConfigProfile, 0, len(files))
	for _, path := range files {
		profile, err := loadConfigProfile(path)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func isConfigProfileExampleFile(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "example.json" || strings.HasSuffix(name, ".example.json")
}

func loadConfigProfile(path string) (ConfigProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ConfigProfile{}, err
	}
	var profile ConfigProfile
	if err := json.Unmarshal(stripJSONComments(data), &profile); err != nil {
		return ConfigProfile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	profile.Path = path
	if err := profile.validate(); err != nil {
		return ConfigProfile{}, err
	}
	return profile, nil
}

func mergeConfigProfiles(base Config, profiles []ConfigProfile) Config {
	if len(profiles) == 0 {
		return base
	}
	if base.Providers == nil {
		base.Providers = make(map[string]ProviderConfig)
	}

	active := profiles[0]
	for _, profile := range profiles {
		provider := profile.ProviderName()
		existing := base.Providers[provider]
		next := profile.providerConfig()
		if next.Type != "" {
			existing.Type = next.Type
		}
		if next.APIKey != "" {
			existing.APIKey = next.APIKey
		}
		if next.BaseURL != "" {
			existing.BaseURL = next.BaseURL
		}
		if len(next.Models) > 0 {
			existing.Models = append([]string(nil), next.Models...)
		}
		if len(next.ExtraBody) > 0 {
			existing.ExtraBody = cloneAnyMap(next.ExtraBody)
		}
		base.Providers[provider] = existing
		if profile.Default {
			active = profile
		}
	}

	base.Provider = active.ProviderName()
	base.ModelName = active.PrimaryModel()
	base.ActiveProfilePath = active.Path
	if active.ContextWindow > 0 {
		base.ContextWindow = active.ContextWindow
	}
	base.Profiles = append([]ConfigProfile(nil), profiles...)
	return base
}

// ApplyConfigProfile 将已加载的 configs 档案应用为当前默认模型配置。
// selector 优先匹配配置文件路径；为兼容内部旧调用，也回退匹配 provider。
func ApplyConfigProfile(base Config, selector string) (Config, ConfigProfile, error) {
	for _, profile := range base.Profiles {
		if profile.Path != selector && profile.ProviderName() != selector {
			continue
		}
		if base.Providers == nil {
			base.Providers = make(map[string]ProviderConfig)
		}
		base.Providers[profile.ProviderName()] = profile.providerConfig()
		base.Provider = profile.ProviderName()
		base.ModelName = profile.PrimaryModel()
		base.ActiveProfilePath = profile.Path
		if profile.ContextWindow > 0 {
			base.ContextWindow = profile.ContextWindow
		}
		return base, profile, nil
	}
	return base, ConfigProfile{}, fmt.Errorf("配置文件不存在：%s", selector)
}

func slugConfigName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.' || r == ' ':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// stripJSONComments 去除 JSON 中的 // 行注释，跟踪引号状态避免误删字符串内容。
func stripJSONComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false

	for i := 0; i < len(data); i++ {
		b := data[i]

		if escaped {
			out = append(out, b)
			escaped = false
			continue
		}

		if inString {
			out = append(out, b)
			if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}

		// 不在字符串内
		if b == '"' {
			inString = true
			out = append(out, b)
			continue
		}

		// 检测 // 注释
		if b == '/' && i+1 < len(data) && data[i+1] == '/' {
			// 跳到行尾
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
			continue
		}

		out = append(out, b)
	}

	return out
}

// WriteStartupError 把启动期致命错误追加写入 ~/.ainovel/last-error.log，并返回
// 该文件路径（best-effort，失败时返回空字符串）。双击启动时控制台窗口会随进程
// 退出立即关闭、错误一闪而过，落盘是这类用户事后追溯的唯一途径。
func WriteStartupError(msg string) string {
	dir := DefaultConfigDir()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, "last-error.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "[%s] %s\n", time.Now().Format(time.RFC3339), msg); err != nil {
		return ""
	}
	return path
}

// SaveConfig 将配置写入指定路径（JSON 格式，缩进美化）。
func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
