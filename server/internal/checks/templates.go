package checks

func f64(x float64) *float64 { return &x }

// Templates are forms most plants keep in some shape, to start one from: a shift's patrol of a
// machine, the first unit off a machine after a start or a changeover, a lab's sample.
func Templates() []Form {
	return []Form{
		{
			Name: "设备点检", Kind: "patrol",
			Note:     "每班开工后两小时内点检一次。看到异常先报班长。",
			Schedule: Schedule{Every: "shift", At: []string{"08:00", "20:00"}, Within: 120},
			Fields: []Field{
				{Key: "oil", Label: "油位", Kind: Choice, Options: []string{"正常", "偏低", "异常"}, Bad: []string{"偏低", "异常"}, Required: true},
				{Key: "noise", Label: "有没有异响", Kind: YesNo, Bad: []string{"是"}, Required: true},
				{Key: "leak", Label: "有没有漏油漏气", Kind: YesNo, Bad: []string{"是"}, Required: true},
				{Key: "gauge_temp", Label: "温度表读数", Kind: Number, Unit: "°C", Hi: f64(80)},
				{Key: "guard", Label: "安全防护", Kind: Choice, Options: []string{"正常", "异常"}, Required: true},
				{Key: "photo", Label: "现场照片", Kind: Photo},
				{Key: "note", Label: "备注", Kind: Text},
			},
		},
		{
			Name: "首件检验", Kind: "first",
			Note:     "开班、换模、调机后的第一件，检验合格才能批量生产。",
			Schedule: Schedule{Every: "shift", At: []string{"08:00", "20:00"}, Within: 60},
			Fields: []Field{
				{Key: "size", Label: "关键尺寸", Kind: Number, Twin: "measure", Required: true},
				{Key: "look", Label: "外观", Kind: Choice, Options: []string{"合格", "不合格"}, Required: true},
				{Key: "verdict", Label: "结论", Kind: Choice, Options: []string{"合格", "不合格"}, Required: true},
				{Key: "photo", Label: "首件照片", Kind: Photo},
				{Key: "note", Label: "备注", Kind: Text},
			},
		},
		{
			Name: "化验", Kind: "lab",
			Note:     "每四小时取样一次，按化验单填。",
			Schedule: Schedule{Every: "hours", Hours: 4, At: []string{"08:00"}},
			Fields: []Field{
				{Key: "hardness", Label: "硬度", Kind: Number, Unit: "HBW", Lo: f64(229), Hi: f64(269), Target: f64(249), Required: true},
				{Key: "carbon", Label: "碳 C", Kind: Number, Unit: "%", Lo: f64(0.42), Hi: f64(0.50), Target: f64(0.46)},
				{Key: "verdict", Label: "判定", Kind: Choice, Options: []string{"合格", "不合格"}, Required: true},
				{Key: "note", Label: "备注", Kind: Text},
			},
		},
	}
}
