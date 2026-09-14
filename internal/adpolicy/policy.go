// Package adpolicy 从策划 JSON 热读取激励广告触发概率；不修改奖励与账号数据。
package adpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
)

var Triggers = []string{"level_entry_potion", "empty_tool", "no_pair", "progress_chest", "level_complete", "daily_gift", "daily_challenge_replay", "season_makeup"}

type Policy struct {
	Version       string         `json:"version"`
	Enabled       bool           `json:"enabled"`
	Probabilities map[string]int `json:"probabilities"`
}

// 中文说明仅供策划读文件，不进入公开接口；旧的无说明配置仍兼容。
type configFile struct {
	Policy
	Comments map[string]string `json:"_说明,omitempty"`
}

func Disabled() Policy { return Policy{Version: "disabled", Probabilities: map[string]int{}} }

// 文件缺失或内容错误时返回关闭自动播放，保留玩家手动观看与正常玩法。
// 每次查询读取文件，原子替换配置后立即生效，无需重启进程。
func Read(path string) Policy {
	file, err := os.Open(path)
	if err != nil {
		return Disabled()
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return Disabled()
	}
	value, err := Parse(data)
	if err != nil {
		return Disabled()
	}
	return value
}

func Parse(data []byte) (Policy, error) {
	var file configFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return Disabled(), err
	}
	value := file.Policy
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Disabled(), errors.New("配置后不能有额外内容")
	}
	if value.Version == "" || len(value.Version) > 64 || len(value.Probabilities) != len(Triggers) {
		return Disabled(), errors.New("version 或节点数量不合法")
	}
	for _, trigger := range Triggers {
		p, ok := value.Probabilities[trigger]
		if !ok || p < 0 || p > 100 {
			return Disabled(), errors.New("每个节点必须配置 0～100 的整数百分比")
		}
	}
	return value, nil
}
