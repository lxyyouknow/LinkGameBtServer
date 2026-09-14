package adpolicy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyHotReloadAndFailClosed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ad-policy.json")
	if Read(p).Enabled {
		t.Fatal("缺配置必须关闭自动广告")
	}
	valid := `{"version":"1","enabled":true,"probabilities":{"level_entry_potion":100,"empty_tool":50,"no_pair":50,"progress_chest":50,"level_complete":50,"daily_gift":50,"daily_challenge_replay":50,"season_makeup":50}}`
	if err := os.WriteFile(p, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if v := Read(p); !v.Enabled || v.Probabilities["level_entry_potion"] != 100 {
		t.Fatal(v)
	}
	for _, invalid := range []string{`{}`, valid + `{}`, `{"version":"1","enabled":true,"probabilities":{"level_entry_potion":101}}`, `null`} {
		if err := os.WriteFile(p, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if Read(p).Enabled {
			t.Fatal("无效配置不能自动播放")
		}
	}
	if err := os.WriteFile(p, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if !Read(p).Enabled {
		t.Fatal("修复配置应热生效")
	}
}

func TestPlannerCommentsDoNotChangeWirePolicy(t *testing.T) {
	data, err := os.ReadFile("../../deploy/config/ad-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := Parse(data)
	if err != nil {
		t.Fatalf("中文说明配置: %+v %v", policy, err)
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "_说明") {
		t.Fatal("策划注释不应下发客户端")
	}
	// 支持注释不能放松实际概率字段的校验。
	var invalidConfig map[string]interface{}
	if err := json.Unmarshal(data, &invalidConfig); err != nil {
		t.Fatal(err)
	}
	invalidConfig["probabilities"].(map[string]interface{})["empty_tool"] = 101
	invalidData, _ := json.Marshal(invalidConfig)
	invalid := string(invalidData)
	if _, err := Parse([]byte(invalid)); err == nil {
		t.Fatal("带注释也必须校验概率范围")
	}
	var wrongField map[string]interface{}
	if err := json.Unmarshal(data, &wrongField); err != nil {
		t.Fatal(err)
	}
	wrongField["enabeld"] = wrongField["enabled"]
	delete(wrongField, "enabled")
	invalidData, _ = json.Marshal(wrongField)
	if _, err := Parse(invalidData); err == nil {
		t.Fatal("带注释也必须拒绝拼错的配置字段")
	}
}
