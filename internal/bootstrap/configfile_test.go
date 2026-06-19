package bootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/errs"
)

const validGlobal = `{
  "provider": "openrouter",
  "model": "google/gemini-2.5-flash",
  "providers": { "openrouter": { "api_key": "sk-test-123456" } }
}`

// writeGlobal 在隔离的 HOME 下写入全局配置，并返回该 HOME。
func writeGlobal(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".ainovel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
			t.Fatalf("write global: %v", err)
		}
	}
	return home
}

// 根因 3：项目级 ./ainovel.json 存在但是坏 JSON，必须报错，不能静默吞掉退回全局。
func TestLoadConfig_CorruptProjectFailsLoud(t *testing.T) {
	writeGlobal(t, validGlobal)
	proj := t.TempDir()
	t.Chdir(proj)
	// 手抄示例多了个尾逗号——最常见的坏 JSON。
	if err := os.WriteFile("ainovel.json", []byte(`{ "model": "x", }`), 0o644); err != nil {
		t.Fatalf("write project: %v", err)
	}

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("坏的 ./ainovel.json 应当报错，却被静默忽略了")
	}
}

// 全局是最低优先级基底：坏文件不得阻断更高优先级的 --config 覆盖（回归守卫——
// 上一版误把全局也 fail-loud，导致"坏全局 + 有效 --config"的用户被无关文件挡住）。
func TestLoadConfig_CorruptGlobalDoesNotBlockOverride(t *testing.T) {
	writeGlobal(t, `{ not json`)
	proj := t.TempDir()
	t.Chdir(proj)
	good := filepath.Join(proj, "good.json")
	if err := os.WriteFile(good, []byte(validGlobal), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	cfg, err := LoadConfig(good)
	if err != nil {
		t.Fatalf("坏全局不应阻断有效 --config，得到: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("应使用 --config 的值，得到 provider=%q", cfg.Provider)
	}
}

// 文件不存在是正常情况（便携/首次），不能报错。
func TestLoadConfig_MissingFilesNoError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // ~/.ainovel/config.json 不存在
	t.Chdir(t.TempDir())   // 也没有 ./ainovel.json

	if _, err := LoadConfig(""); err != nil {
		t.Fatalf("缺失配置文件不应报错，得到: %v", err)
	}
}

// 正常路径：全局 + 项目级合并生效。
func TestLoadConfig_ValidMergeWorks(t *testing.T) {
	writeGlobal(t, validGlobal)
	proj := t.TempDir()
	t.Chdir(proj)
	if err := os.WriteFile("ainovel.json", []byte(`{ "model": "google/gemini-2.5-pro" }`), 0o644); err != nil {
		t.Fatalf("write project: %v", err)
	}

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("有效配置不应报错: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("provider 应保留全局值 openrouter，得到 %q", cfg.Provider)
	}
	if cfg.ModelName != "google/gemini-2.5-pro" {
		t.Errorf("model 应被项目级覆盖，得到 %q", cfg.ModelName)
	}
}

func TestLoadConfig_ProjectProfilesUseCamelFields(t *testing.T) {
	writeGlobal(t, "")
	proj := t.TempDir()
	t.Chdir(proj)
	if err := os.MkdirAll("configs", 0o755); err != nil {
		t.Fatalf("mkdir configs: %v", err)
	}
	content := `{
  "name": "WX API Deepseek",
  "provider": "wx-api",
  "type": "openai",
  "baseUrl": "https://ai.wx-api.online/v1",
  "apiKey": "sk-test-profile",
  "model": "Deepseek-V4-Max",
  "models": ["Deepseek-V4-Max"],
  "default": true
}`
	if err := os.WriteFile(filepath.Join("configs", "wx-api.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("configs 配置应可加载: %v", err)
	}
	if cfg.Provider != "wx-api" || cfg.ModelName != "Deepseek-V4-Max" {
		t.Fatalf("默认配置应来自 configs，provider=%q model=%q", cfg.Provider, cfg.ModelName)
	}
	pc := cfg.Providers["wx-api"]
	if pc.Type != "openai" {
		t.Fatalf("type 应解析为 openai，得到 %q", pc.Type)
	}
	if pc.BaseURL != "https://ai.wx-api.online/v1" {
		t.Fatalf("baseUrl 未解析，得到 %q", pc.BaseURL)
	}
	if pc.APIKey != "sk-test-profile" {
		t.Fatal("apiKey 未解析")
	}
	if len(cfg.Profiles) != 1 || cfg.Profiles[0].DisplayName() != "WX API Deepseek" {
		t.Fatalf("应保留配置档案供运行中切换，得到 %+v", cfg.Profiles)
	}
}

func TestLoadConfig_ProjectProfileBadJSONFails(t *testing.T) {
	writeGlobal(t, validGlobal)
	proj := t.TempDir()
	t.Chdir(proj)
	if err := os.MkdirAll("configs", 0o755); err != nil {
		t.Fatalf("mkdir configs: %v", err)
	}
	if err := os.WriteFile(filepath.Join("configs", "bad.json"), []byte(`{ "provider": "x", }`), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("坏的 configs/*.json 应当报错")
	}
}

func TestLoadConfigProfiles_SkipsExampleJSON(t *testing.T) {
	proj := t.TempDir()
	dir := filepath.Join(proj, "configs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir configs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "example.json"), []byte(`{
  "provider": "解释文字",
  "default": "真实配置里这里应是 bool，示例文件不应被解析"
}`), 0o644); err != nil {
		t.Fatalf("write example: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.json"), []byte(`{
  "provider": "real",
  "type": "openai",
  "apiKey": "sk-test",
  "baseUrl": "https://api.example.com/v1",
  "model": "real-model"
}`), 0o644); err != nil {
		t.Fatalf("write real: %v", err)
	}

	profiles, err := LoadConfigProfiles(dir)
	if err != nil {
		t.Fatalf("example.json 应被跳过，得到错误: %v", err)
	}
	if len(profiles) != 1 || profiles[0].ProviderName() != "real" {
		t.Fatalf("应只加载真实配置，得到 %+v", profiles)
	}
}

// 根因 2（issue #37 核心复现）：项目级覆盖 provider 但没声明对应 providers 凭证，
// ValidateBase 必须报 config 错误（而非放行后在更深处崩溃）。
func TestValidateBase_ProviderOverrideWithoutCredentials(t *testing.T) {
	cfg := Config{
		Provider:  "mimo",
		ModelName: "mimo-v2.5-pro",
		Providers: map[string]ProviderConfig{
			"openrouter": {APIKey: "sk-test-123456"},
		},
	}
	cfg.FillDefaults()
	err := cfg.ValidateBase()
	if err == nil {
		t.Fatal("provider 缺凭证应报错")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("应包装 errs.ErrConfig，得到: %v", err)
	}
}

// 内置示例（go:embed 的 config.example.jsonc）必须自洽：去注释后是合法 JSON、
// 顶层 provider 指针不悬空、且点破了“指针”心智——它是用户照抄的样板，自己坏了就坑人。
func TestExampleConfigIsValidAndSelfConsistent(t *testing.T) {
	if exampleConfig == "" {
		t.Fatal("go:embed 未生效，exampleConfig 为空")
	}
	var cfg Config
	if err := json.Unmarshal(stripJSONComments([]byte(exampleConfig)), &cfg); err != nil {
		t.Fatalf("内置示例去注释后不是合法 JSON（用户照抄即坑）: %v", err)
	}
	if cfg.Provider == "" || cfg.ModelName == "" {
		t.Fatal("示例应给出默认 provider/model")
	}
	if _, ok := cfg.Providers[cfg.Provider]; !ok {
		t.Errorf("示例顶层 provider %q 未指向 providers 中的条目——指针正面样板自己悬空了", cfg.Provider)
	}
	if !contains(exampleConfig, "指针") {
		t.Error("示例应点破“provider 是指针”——别让 #37 的认知陷阱回潮")
	}
}

func TestWriteStartupError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := WriteStartupError("boom: provider not configured")
	if path == "" {
		t.Fatal("应返回落盘路径")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 last-error.log: %v", err)
	}
	if want := "boom: provider not configured"; !contains(string(data), want) {
		t.Errorf("日志应包含 %q，实际: %s", want, data)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
