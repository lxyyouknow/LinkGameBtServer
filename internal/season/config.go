package season

import "fmt"

const ConfigVersion = "original-season-v1"

// DayRewardConfig 是原版 season_data.json 中单日奖励的服务端版本化镜像。
type DayRewardConfig struct {
	Theme   int
	Hint    int
	Shuffle int
	Remove  int
}

type Config struct {
	ID        int
	Month     int
	Name      string
	ThemeID   int
	CowSkinID int
	Days      [RewardDayCount]DayRewardConfig
}

var seasonMetadata = [...]struct {
	month     int
	name      string
	themeID   int
	cowSkinID int
}{
	{5, "五月·云游", 17, 18},
	{6, "六月·童趣", 20, 21},
	{7, "七月·音浪", 25, 27},
	{8, "八月·食神", 32, 32},
	{9, "九月·梦境", 37, 37},
	{10, "十月·金秋", 43, 43},
	{11, "十一月·捣蛋", 47, 47},
	{12, "十二月·暖冬", 50, 51},
	{1, "一月·新年", 53, 56},
	{2, "二月·浪漫", 56, 60},
	{3, "三月·绿野", 57, 61},
	{4, "四月·愚乐", 58, 62},
}

func ConfigForMonth(month int) (Config, error) {
	for id, metadata := range seasonMetadata {
		if metadata.month != month {
			continue
		}
		return Config{
			ID: id, Month: metadata.month, Name: metadata.name,
			ThemeID: metadata.themeID, CowSkinID: metadata.cowSkinID,
			Days: originalRewardDays(),
		}, nil
	}
	return Config{}, fmt.Errorf("%w: month=%d", ErrConfigUnavailable, month)
}

func AllConfigs() []Config {
	configs := make([]Config, 0, len(seasonMetadata))
	for _, metadata := range seasonMetadata {
		config, err := ConfigForMonth(metadata.month)
		if err != nil {
			panic(err)
		}
		configs = append(configs, config)
	}
	return configs
}

func originalRewardDays() [RewardDayCount]DayRewardConfig {
	var days [RewardDayCount]DayRewardConfig
	for index := range days {
		days[index].Theme = 1
	}
	for _, day := range []int{5, 10, 15, 20, 25} {
		themeCount := 3
		if day == 5 {
			themeCount = 2
		}
		days[day-1] = DayRewardConfig{Theme: themeCount, Hint: 3, Shuffle: 3, Remove: 3}
	}
	return days
}

func RewardDays(config Config) ([]RewardDay, error) {
	if config.ID < 0 || config.ID >= len(seasonMetadata) || config.Month < 1 || config.Month > 12 {
		return nil, ErrConfigUnavailable
	}
	rewardDays := make([]RewardDay, 0, RewardDayCount)
	fragmentIndex := 0
	for index, day := range config.Days {
		rewards := make([]Reward, 0, day.Theme+3)
		for count := 0; count < day.Theme; count++ {
			if fragmentIndex >= 34 {
				return nil, ErrConfigUnavailable
			}
			rewards = append(rewards, Reward{
				Type: "theme_fragment", ThemeID: intPointer(config.ThemeID),
				FragmentIndex: intPointer(fragmentIndex), Quantity: 1,
			})
			fragmentIndex++
		}
		appendProp := func(propType string, quantity int) {
			if quantity > 0 {
				rewards = append(rewards, Reward{Type: "prop", PropType: propType, Quantity: int64(quantity)})
			}
		}
		appendProp("hint", day.Hint)
		appendProp("shuffle", day.Shuffle)
		appendProp("remove", day.Remove)
		rewardDays = append(rewardDays, RewardDay{Day: index + 1, Rewards: rewards})
	}
	if fragmentIndex != 34 || len(rewardDays) != RewardDayCount {
		return nil, ErrConfigUnavailable
	}
	return rewardDays, nil
}

func intPointer(value int) *int {
	copy := value
	return &copy
}
