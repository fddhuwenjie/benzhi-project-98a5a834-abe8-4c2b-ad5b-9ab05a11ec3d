package risk

import (
	"fmt"
	"heritage-care/internal/domain"
	"math"
	"sort"
	"strings"
)

const RuleVersion = "risk-v2.0"

type Input struct {
	Material, Sensitivity, Location string
	Temperature                     *float64
	Humidity                        *float64
	Illuminance                     *float64
	HistoricalIssues                []string
}

func materialGroup(material string) string {
	m := strings.ToLower(strings.TrimSpace(material))
	switch {
	case strings.Contains(m, "paper"), strings.Contains(m, "纸"):
		return "paper"
	case strings.Contains(m, "silk"), strings.Contains(m, "textile"), strings.Contains(m, "丝"), strings.Contains(m, "织"):
		return "silk"
	case strings.Contains(m, "wood"), strings.Contains(m, "木"):
		return "wood"
	case strings.Contains(m, "metal"), strings.Contains(m, "金属"), strings.Contains(m, "铜"), strings.Contains(m, "铁"):
		return "metal"
	default:
		return "other"
	}
}

func highSensitivity(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	return v == "high" || v == "high_sensitive" || strings.Contains(v, "高") || strings.Contains(v, "敏感")
}

func Thresholds(material, sensitivity string) domain.ThresholdSet {
	var t domain.ThresholdSet
	switch materialGroup(material) {
	case "paper":
		t = domain.ThresholdSet{TemperatureMin: 18, TemperatureMax: 22, HumidityMin: 45, HumidityMax: 58, IlluminanceMax: 50}
	case "silk":
		t = domain.ThresholdSet{TemperatureMin: 18, TemperatureMax: 22, HumidityMin: 45, HumidityMax: 58, IlluminanceMax: 50}
	case "wood":
		t = domain.ThresholdSet{TemperatureMin: 16, TemperatureMax: 24, HumidityMin: 45, HumidityMax: 60, IlluminanceMax: 150}
	case "metal":
		t = domain.ThresholdSet{TemperatureMin: 16, TemperatureMax: 26, HumidityMin: 30, HumidityMax: 60, IlluminanceMax: 200}
	default:
		t = domain.ThresholdSet{TemperatureMin: 18, TemperatureMax: 22, HumidityMin: 40, HumidityMax: 55, IlluminanceMax: 100}
	}
	if highSensitivity(sensitivity) {
		switch materialGroup(material) {
		case "paper", "silk":
			t.TemperatureMax, t.HumidityMax, t.IlluminanceMax = 21, 55, 35
		case "wood":
			t.TemperatureMin, t.TemperatureMax, t.HumidityMin, t.HumidityMax, t.IlluminanceMax = 18, 22, 48, 58, 100
		case "metal":
			t.TemperatureMin, t.TemperatureMax, t.HumidityMin, t.HumidityMax, t.IlluminanceMax = 18, 24, 35, 55, 150
		default:
			t.TemperatureMin, t.TemperatureMax, t.HumidityMin, t.HumidityMax, t.IlluminanceMax = 18, 21, 42, 52, 75
		}
	}
	t.TemperatureUnit, t.HumidityUnit, t.IlluminanceUnit = "celsius", "percent_rh", "lux"
	return t
}

func thresholdText(t domain.ThresholdSet) string {
	return fmt.Sprintf("温度%.1f-%.1f℃；湿度%.1f%%-%.1f%%RH；照度≤%.1flx", t.TemperatureMin, t.TemperatureMax, t.HumidityMin, t.HumidityMax, t.IlluminanceMax)
}

func Assess(in Input) domain.RiskSnapshot {
	t := Thresholds(in.Material, in.Sensitivity)
	details := []domain.RiskScoreDetail{}
	total := 0
	add := func(code, metric, hit, threshold string, score int, explanation string) {
		if score <= 0 || total >= 100 {
			return
		}
		if total+score > 100 {
			score = 100 - total
		}
		total += score
		details = append(details, domain.RiskScoreDetail{RuleCode: code, Metric: metric, HitValue: hit, Threshold: threshold, Score: score, Explanation: explanation})
	}
	group := materialGroup(in.Material)
	base := 5
	if group == "paper" || group == "silk" || group == "wood" {
		base = 10
	}
	if highSensitivity(in.Sensitivity) {
		base += 8
	}
	explanation := "材质敏感度基础风险"
	if group == "other" {
		explanation = "未知或其他材质采用保守默认阈值"
	}
	add("material_sensitivity", "material", strings.TrimSpace(in.Material)+"/"+strings.TrimSpace(in.Sensitivity), thresholdText(t), base, explanation)

	if in.Temperature != nil {
		deviation := 0.0
		if *in.Temperature < t.TemperatureMin {
			deviation = t.TemperatureMin - *in.Temperature
		}
		if *in.Temperature > t.TemperatureMax {
			deviation = *in.Temperature - t.TemperatureMax
		}
		if deviation > 0 {
			add("temperature_deviation", "temperature", fmt.Sprintf("%.2f", *in.Temperature), fmt.Sprintf("%.1f-%.1f celsius", t.TemperatureMin, t.TemperatureMax), min(20, 5+int(math.Ceil(deviation*3))), "温度偏离适用范围，按偏差幅度计分")
		}
	}
	if in.Humidity != nil {
		deviation := 0.0
		if *in.Humidity < t.HumidityMin {
			deviation = t.HumidityMin - *in.Humidity
		}
		if *in.Humidity > t.HumidityMax {
			deviation = *in.Humidity - t.HumidityMax
		}
		if deviation > 0 {
			add("humidity_deviation", "humidity", fmt.Sprintf("%.2f", *in.Humidity), fmt.Sprintf("%.1f-%.1f percent_rh", t.HumidityMin, t.HumidityMax), min(30, 5+int(math.Ceil(deviation*1.5))), "相对湿度偏离适用范围，按偏差幅度计分")
		}
	}
	if in.Illuminance != nil && *in.Illuminance > t.IlluminanceMax {
		ratio := (*in.Illuminance - t.IlluminanceMax) / max(t.IlluminanceMax, 1)
		add("illuminance_deviation", "illuminance", fmt.Sprintf("%.2f", *in.Illuminance), fmt.Sprintf("≤%.1f lux", t.IlluminanceMax), min(25, 5+int(math.Ceil(ratio*15))), "照度超过适用上限，按超出比例计分")
	}

	counts := map[string]int{}
	for _, issue := range in.HistoricalIssues {
		key := strings.ToLower(strings.TrimSpace(issue))
		if key == "" {
			key = "unspecified"
		}
		counts[key]++
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		count := counts[key]
		score := 4 * count
		if count > 1 {
			score += 3 * (count - 1)
		}
		add("historical_issue_"+key, "historical_issue", key, fmt.Sprintf("重复%d次", count), min(20, score), "历史异常按类型和重复次数计分")
	}

	level := domain.RiskLow
	if total >= 60 {
		level = domain.RiskHigh
	} else if total >= 30 {
		level = domain.RiskMedium
	}
	textDetails := make([]string, 0, len(details))
	for _, detail := range details {
		textDetails = append(textDetails, detail.Explanation)
	}
	return domain.RiskSnapshot{Level: level, Score: total, RuleVersion: RuleVersion, Details: textDetails, ScoreDetails: details, Thresholds: t}
}

func Checklist(level domain.RiskLevel, material string, thresholds domain.ThresholdSet) []domain.ChecklistItem {
	items := []domain.ChecklistItem{
		{Code: "environment", Label: "温湿度与照度", Threshold: thresholdText(thresholds), Thresholds: thresholds},
		{Code: "appearance", Label: "外观完整性", Threshold: "无裂隙、霉斑、虫蛀、锈蚀或变形", Thresholds: thresholds},
	}
	group := materialGroup(material)
	if level != domain.RiskLow || group == "paper" || group == "silk" || group == "wood" {
		items = append(items, domain.ChecklistItem{Code: "material", Label: "材质脆弱点", Threshold: "检查材质特有的起翘、粉化、腐蚀和结构松动", Thresholds: thresholds})
	}
	if level == domain.RiskHigh {
		items = append(items, domain.ChecklistItem{Code: "emergency", Label: "应急预案", Threshold: "确认隔离、遮光和转移预案可立即执行", Thresholds: thresholds})
	}
	return items
}
