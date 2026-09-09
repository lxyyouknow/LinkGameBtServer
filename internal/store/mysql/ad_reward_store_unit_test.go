package mysql

import (
	"errors"
	"testing"

	"linkgame-server/internal/adreward"
)

func TestParseSeasonMakeupBusinessKey严格校验(t *testing.T) {
	seasonKey, day, err := parseSeasonMakeupBusinessKey("season-makeup:2026-09:1")
	if err != nil || seasonKey != "2026-09" || day != 1 {
		t.Fatalf("解析结果=%q/%d err=%v", seasonKey, day, err)
	}
	for _, value := range []string{
		"season-makeup:2026-9:1",
		"season-makeup:2026-09:01",
		"season-makeup:2026-09:0",
		"season-makeup:2026-09:26",
		"season-makeup:2026-13:1",
		"season_makeup:2026-09:1",
	} {
		if _, _, err := parseSeasonMakeupBusinessKey(value); !errors.Is(err, adreward.ErrInvalidRequest) {
			t.Fatalf("%q error=%v", value, err)
		}
	}
}
