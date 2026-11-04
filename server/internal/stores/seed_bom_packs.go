package stores

import (
	"fmt"
	"slices"
	"time"
)

// The other demo companies' BOMs. Each is keyed by the company's id and written against the part
// numbers its industry pack gives its products (web/src/config/industries); the stores it needs
// (raw material, bought-in and standard parts, consumables, packing) are filled with their stock,
// one item below its safety stock and some too little for a big order, so the BOM page, the
// kitting of the open orders and the production plan's material hold have something to show.
// A sub-assembly (an evaporator, a jelly roll, a dough, a body in white) is a part with a BOM of
// its own, which the finished part's line points at by code. A part or two has a second revision
// with its change record, and one a draft waiting for approval.

type pItem struct {
	cat, code, name, spec, use, unit string
	safety, stock                    float64
	place                            string
}

func pi(cat, code, name, spec, use, unit string, safety, stock float64, place string) pItem {
	return pItem{cat, code, name, spec, use, unit, safety, stock, place}
}

type pBOM struct {
	part, name, drawing, unit, note string
	lines                           []BOMLine
}

// pChange is a part's next revision: edit changes the copy of the one in effect; keep leaves it a
// draft instead of putting it in effect.
type pChange struct {
	part, why, drawing, note string
	edit                     func(b *BOM)
	keep                     bool
}

type pack struct {
	eng, chief, keeper string
	cats               []Category
	items              []pItem
	boms               []pBOM
	changes            []pChange
}

// ln is one line of a BOM.
func ln(kind, code, name, spec string, qty float64, unit string, scrap float64, op string) BOMLine {
	return BOMLine{Code: code, Name: name, Kind: kind, Spec: spec, Qty: qty, Unit: unit, Scrap: scrap, Op: op}
}

// setQty and setScrap change a line of a revision by its code.
func setQty(b *BOM, code string, qty float64) {
	for i := range b.Lines {
		if b.Lines[i].Code == code {
			b.Lines[i].Qty = qty
		}
	}
}

func setScrap(b *BOM, code string, scrap float64) {
	for i := range b.Lines {
		if b.Lines[i].Code == code {
			b.Lines[i].Scrap = scrap
		}
	}
}

func dropLine(b *BOM, code string) {
	b.Lines = slices.DeleteFunc(b.Lines, func(l BOMLine) bool { return l.Code == code })
}

var stdCats = []Category{
	{Key: "raw", Label: "原材料", Kind: Other, Unit: "kg", Fields: []string{"规格", "用在"}},
	{Key: "buy", Label: "外购件", Kind: Part, Unit: "件", Fields: []string{"规格", "用在"}},
	{Key: "std", Label: "标准件", Kind: Part, Unit: "件", Fields: []string{"规格", "用在"}},
	{Key: "aux", Label: "辅料", Kind: Other, Unit: "kg", Fields: []string{"规格", "用在"}},
}

var foodCats = []Category{
	{Key: "raw", Label: "食材", Kind: Other, Unit: "kg", Fields: []string{"规格", "用在"}},
	{Key: "pack", Label: "包材", Kind: Part, Unit: "件", Fields: []string{"规格", "用在"}},
	{Key: "aux", Label: "辅料", Kind: Other, Unit: "kg", Fields: []string{"规格", "用在"}},
}

// packs are the demo companies other than the one with the forging stores, by company id.
var packs = map[string]pack{
	"jinggong": {
		eng: "技术部 吴工", chief: "总工 郑志远", keeper: "仓管 周丽", cats: stdCats,
		items: []pItem{
			pi("raw", "GB-Q235B-6", "Q235B 钢板 δ6", "δ6×1500×6000", "支架焊接本体", "kg", 3000, 6500, "原料库 A1"),
			pi("raw", "GB-Q235B-SQ40", "Q235B 方管 40×40×3", "40×40×3", "支架焊接本体", "m", 1500, 5200, "原料库 A2"),
			pi("buy", "PI-DG112", "端盖铸件毛坯 HT200", "HT200 φ190", "电机端盖", "件", 1500, 4200, "毛坯库 B1"),
			pi("buy", "WG-XJD-30", "减震橡胶垫", "φ30×12", "支架焊合件", "件", 1000, 2600, "外购件库 C1"),
			pi("std", "BZ-6205", "深沟球轴承 6205-2RS", "GB/T 276", "电机端盖", "件", 1500, 3600, "标准件库 D1"),
			pi("std", "BZ-TC254", "骨架油封 TC", "25×47×7", "电机端盖", "件", 1500, 1180, "标准件库 D2"),
			pi("std", "BZ-M6x16", "内六角圆柱头螺钉 M6×16", "GB/T 70.1", "电机端盖", "件", 8000, 21000, "标准件库 D3"),
			pi("std", "BZ-JQ52", "孔用弹性挡圈 52", "GB/T 893", "电机端盖", "件", 1500, 5200, "标准件库 D4"),
			pi("std", "BZ-NUT-M8", "焊接螺母 M8", "GB/T 13680", "支架焊合件", "件", 6000, 14000, "标准件库 D5"),
			pi("aux", "FL-QXY", "水基切削液", "5% 稀释", "机加", "kg", 200, 760, "辅料库 E1"),
			pi("aux", "FL-FXY", "防锈油", "薄层型", "成品包装", "kg", 60, 180, "辅料库 E2"),
			pi("aux", "FL-HS", "焊丝 ER50-6", "φ1.0", "焊接", "kg", 150, 420, "辅料库 E3"),
			pi("aux", "FL-PSF", "环氧聚酯喷塑粉", "RAL 7035", "喷塑", "kg", 120, 340, "辅料库 E4"),
			pi("aux", "FL-CTN", "瓦楞纸箱 12 件装", "五层", "成品包装", "件", 400, 1100, "辅料库 E5"),
			pi("aux", "FL-QPD", "气泡袋", "200×300", "成品包装", "件", 1000, 3500, "辅料库 E6"),
		},
		boms: []pBOM{
			{part: "JG-ZJ-320-H", name: "支架焊接本体", drawing: "A", note: "CO2 保护焊，焊后去应力", lines: []BOMLine{
				ln(Raw, "GB-Q235B-6", "Q235B 钢板", "δ6", 2.85, "kg", 4, "激光下料"),
				ln(Raw, "GB-Q235B-SQ40", "Q235B 方管", "40×40×3", 0.46, "m", 2, "切管"),
				ln(Aux, "FL-HS", "焊丝 ER50-6", "φ1.0", 0.045, "kg", 0, "焊接"),
			}},
			{part: "JG-ZJ-320", name: "支架焊合件", drawing: "B", lines: []BOMLine{
				ln(Make, "JG-ZJ-320-H", "支架焊接本体", "", 1, "件", 0, "焊接"),
				ln(Std, "BZ-NUT-M8", "焊接螺母 M8", "GB/T 13680", 4, "件", 0, "凸焊"),
				ln(Buy, "WG-XJD-30", "减震橡胶垫", "φ30×12", 2, "件", 0, "装配"),
				ln(Aux, "FL-PSF", "环氧聚酯喷塑粉", "", 0.12, "kg", 8, "喷塑"),
				ln(Aux, "FL-QPD", "气泡袋", "", 1, "件", 0, "包装"),
			}},
			{part: "JG-DG-112", name: "电机端盖", drawing: "B", note: "止口 φ180 H7，轴承位 φ52 H7", lines: []BOMLine{
				ln(Buy, "PI-DG112", "端盖铸件毛坯 HT200", "", 1, "件", 2, "粗车"),
				ln(Std, "BZ-6205", "深沟球轴承 6205-2RS", "", 1, "件", 0, "压装"),
				ln(Std, "BZ-TC254", "骨架油封 TC", "25×47×7", 1, "件", 0, "压装"),
				ln(Std, "BZ-M6x16", "内六角圆柱头螺钉 M6×16", "GB/T 70.1", 4, "件", 0, "装配"),
				ln(Std, "BZ-JQ52", "孔用弹性挡圈 52", "GB/T 893", 1, "件", 0, "装配"),
				ln(Aux, "FL-QXY", "水基切削液", "", 0.06, "kg", 0, "机加"),
				ln(Aux, "FL-FXY", "防锈油", "", 0.004, "kg", 0, "包装"),
				ln(Aux, "FL-CTN", "瓦楞纸箱 12 件装", "", 0.0833, "件", 0, "包装"),
			}},
		},
		changes: []pChange{
			{part: "JG-DG-112", why: "改用专用夹具，加工余量由 3 mm 减到 2.2 mm，毛坯损耗和切削液都降下来", drawing: "C", note: "图纸 C 版：止口余量减小",
				edit: func(b *BOM) { setScrap(b, "PI-DG112", 1.2); setQty(b, "FL-QXY", 0.045) }},
			{part: "JG-ZJ-320", why: "客户加了 2 个固定点，焊接螺母由 4 个改 6 个", drawing: "C", note: "图纸 C 版：加 2 个固定点",
				edit: func(b *BOM) { setQty(b, "BZ-NUT-M8", 6) }},
			{part: "JG-ZJ-320-H", why: "加强筋加厚，钢板用量上调", keep: true, edit: func(b *BOM) { setQty(b, "GB-Q235B-6", 3.05) }},
		},
	},

	"nanfang": {
		eng: "技术部 梁工", chief: "总工 陈国梁", keeper: "仓管 黎彩霞", cats: stdCats,
		items: []pItem{
			pi("raw", "CU-TP2-7", "紫铜管 TP2", "φ7×0.25", "蒸发器、冷凝器", "m", 30000, 120000, "原料库 A1"),
			pi("raw", "AL-FIN-105", "亲水铝箔", "0.105 mm", "蒸发器、冷凝器", "kg", 2000, 9500, "原料库 A2"),
			pi("raw", "GALV-SGCC-08", "镀锌钢板 SGCC", "0.8 mm", "外机钣金", "kg", 4000, 12500, "原料库 A3"),
			pi("raw", "CL-R32", "制冷剂 R32", "钢瓶 600 kg", "外机充注", "kg", 600, 520, "危化品库 H1"),
			pi("buy", "WG-UB-7", "U 型铜弯头", "φ7", "换热器", "件", 20000, 56000, "外购件库 C1"),
			pi("buy", "WG-QF-GL", "贯流风叶", "φ98×650", "内机", "件", 3000, 8000, "外购件库 C2"),
			pi("buy", "WG-DJ-IDU", "内机风机电机", "DC 20 W", "内机", "件", 3000, 6800, "外购件库 C3"),
			pi("buy", "WG-BJ-24", "导风板步进电机", "DC 12 V", "内机", "件", 4000, 9200, "外购件库 C4"),
			pi("buy", "WG-PCB-N1", "内机主控板", "N1 平台", "内机", "件", 2500, 5200, "外购件库 C5"),
			pi("buy", "WG-PCB-DIS", "显示板", "N1", "内机", "件", 3000, 7400, "外购件库 C6"),
			pi("buy", "WG-MB-35", "内机面板", "白色", "内机", "件", 2500, 5600, "外购件库 C7"),
			pi("buy", "WG-DK-35", "内机底壳", "35 机型", "内机", "件", 2500, 5400, "外购件库 C8"),
			pi("buy", "WG-DF-35", "导风板", "35 机型", "内机", "件", 3000, 7000, "外购件库 C9"),
			pi("buy", "WG-LW-35", "滤网", "35 机型", "内机", "件", 6000, 14000, "外购件库 C10"),
			pi("buy", "WG-YK-N1", "遥控器", "N1", "内机", "件", 3000, 7600, "外购件库 C11"),
			pi("buy", "WG-YSJ-14", "变频转子压缩机", "14 cc", "外机", "件", 1500, 2100, "外购件库 C12"),
			pi("buy", "WG-ZF-ODU", "轴流风叶", "φ400", "外机", "件", 1500, 3800, "外购件库 C13"),
			pi("buy", "WG-DJ-ODU", "外机风机电机", "DC 40 W", "外机", "件", 1500, 3900, "外购件库 C14"),
			pi("buy", "WG-SV-4W", "四通换向阀", "", "外机", "件", 1500, 3600, "外购件库 C15"),
			pi("buy", "WG-EEV", "电子膨胀阀", "", "外机", "件", 1500, 3300, "外购件库 C16"),
			pi("buy", "WG-PCB-ODU", "外机主板", "N1 平台", "外机", "件", 1500, 3500, "外购件库 C17"),
			pi("std", "BZ-AAA", "AAA 碱性电池", "LR03", "遥控器", "件", 6000, 16000, "标准件库 D1"),
			pi("std", "BZ-ST39x16", "自攻螺钉 ST3.9×16", "GB/T 845", "内外机装配", "件", 50000, 160000, "标准件库 D2"),
			pi("std", "BZ-JZF-6", "截止阀", "φ6.35", "外机", "件", 3000, 7000, "标准件库 D3"),
			pi("std", "BZ-JZF-10", "截止阀", "φ9.52", "外机", "件", 3000, 7000, "标准件库 D4"),
			pi("aux", "FL-BWM", "保温棉", "B1 级", "内机", "kg", 400, 1500, "辅料库 E1"),
			pi("aux", "FL-PSF-N", "外机喷塑粉", "RAL 9003", "外机钣金", "kg", 300, 900, "辅料库 E2"),
			pi("aux", "FL-CX-IDU", "内机彩箱", "", "包装", "件", 3000, 7200, "辅料库 E3"),
			pi("aux", "FL-PM-IDU", "内机泡沫垫", "EPS", "包装", "件", 3000, 7100, "辅料库 E4"),
			pi("aux", "FL-CX-ODU", "外机彩箱", "", "包装", "件", 1500, 3900, "辅料库 E5"),
			pi("aux", "FL-PM-ODU", "外机泡沫垫", "EPS", "包装", "件", 1500, 3800, "辅料库 E6"),
		},
		boms: []pBOM{
			{part: "NF-EV-35", name: "35 机型蒸发器", drawing: "A", lines: []BOMLine{
				ln(Raw, "CU-TP2-7", "紫铜管 TP2", "φ7×0.25", 11.2, "m", 1.5, "胀管"),
				ln(Raw, "AL-FIN-105", "亲水铝箔", "0.105 mm", 0.62, "kg", 3, "冲片"),
				ln(Buy, "WG-UB-7", "U 型铜弯头", "φ7", 14, "件", 0, "焊接"),
			}},
			{part: "NF-CD-35", name: "35 机型冷凝器", drawing: "A", lines: []BOMLine{
				ln(Raw, "CU-TP2-7", "紫铜管 TP2", "φ7×0.25", 22, "m", 1.5, "胀管"),
				ln(Raw, "AL-FIN-105", "亲水铝箔", "0.105 mm", 1.05, "kg", 3, "冲片"),
				ln(Buy, "WG-UB-7", "U 型铜弯头", "φ7", 28, "件", 0, "焊接"),
			}},
			{part: "NF-IDU-35GW", name: "KFR-35GW 挂机内机", drawing: "B", lines: []BOMLine{
				ln(Make, "NF-EV-35", "35 机型蒸发器", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-QF-GL", "贯流风叶", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-DJ-IDU", "内机风机电机", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-BJ-24", "导风板步进电机", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-PCB-N1", "内机主控板", "", 1, "件", 0, "电控装配"),
				ln(Buy, "WG-PCB-DIS", "显示板", "", 1, "件", 0, "电控装配"),
				ln(Buy, "WG-MB-35", "内机面板", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-DK-35", "内机底壳", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-DF-35", "导风板", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-LW-35", "滤网", "", 2, "件", 0, "总装"),
				ln(Buy, "WG-YK-N1", "遥控器", "", 1, "件", 0, "包装"),
				ln(Std, "BZ-AAA", "AAA 碱性电池", "LR03", 2, "件", 0, "包装"),
				ln(Std, "BZ-ST39x16", "自攻螺钉 ST3.9×16", "GB/T 845", 12, "件", 0, "总装"),
				ln(Aux, "FL-BWM", "保温棉", "", 0.08, "kg", 5, "总装"),
				ln(Aux, "FL-CX-IDU", "内机彩箱", "", 1, "件", 0, "包装"),
				ln(Aux, "FL-PM-IDU", "内机泡沫垫", "", 1, "件", 0, "包装"),
			}},
			{part: "NF-ODU-35W", name: "KFR-35W 空调外机", drawing: "B", lines: []BOMLine{
				ln(Make, "NF-CD-35", "35 机型冷凝器", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-YSJ-14", "变频转子压缩机", "14 cc", 1, "件", 0, "总装"),
				ln(Buy, "WG-ZF-ODU", "轴流风叶", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-DJ-ODU", "外机风机电机", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-SV-4W", "四通换向阀", "", 1, "件", 0, "管路焊接"),
				ln(Buy, "WG-EEV", "电子膨胀阀", "", 1, "件", 0, "管路焊接"),
				ln(Std, "BZ-JZF-6", "截止阀", "φ6.35", 1, "件", 0, "管路焊接"),
				ln(Std, "BZ-JZF-10", "截止阀", "φ9.52", 1, "件", 0, "管路焊接"),
				ln(Buy, "WG-PCB-ODU", "外机主板", "", 1, "件", 0, "电控装配"),
				ln(Raw, "GALV-SGCC-08", "镀锌钢板 SGCC", "0.8 mm", 6.8, "kg", 6, "冲压"),
				ln(Aux, "FL-PSF-N", "外机喷塑粉", "", 0.18, "kg", 8, "喷塑"),
				ln(Raw, "CL-R32", "制冷剂 R32", "", 0.55, "kg", 2, "充注"),
				ln(Std, "BZ-ST39x16", "自攻螺钉 ST3.9×16", "GB/T 845", 18, "件", 0, "总装"),
				ln(Aux, "FL-CX-ODU", "外机彩箱", "", 1, "件", 0, "包装"),
				ln(Aux, "FL-PM-ODU", "外机泡沫垫", "", 1, "件", 0, "包装"),
			}},
		},
		changes: []pChange{
			{part: "NF-ODU-35W", why: "冷凝器管路缩短，R32 充注量由 550 g 降到 520 g", drawing: "C", note: "图纸 C 版：管路缩短",
				edit: func(b *BOM) { setQty(b, "CL-R32", 0.52) }},
			{part: "NF-EV-35", why: "翅片开窗改进，铝箔用量降 6%", edit: func(b *BOM) { setQty(b, "AL-FIN-105", 0.58) }},
			{part: "NF-IDU-35GW", why: "主控板换 N2 平台，待样机通过 3C 后生效", keep: true, edit: func(b *BOM) {
				for i := range b.Lines {
					if b.Lines[i].Code == "WG-PCB-N1" {
						b.Lines[i].Code, b.Lines[i].Spec, b.Lines[i].Note = "WG-PCB-N2", "N2 平台", "样机在送检"
					}
				}
			}},
		},
	},

	"ruineng": {
		eng: "技术部 孙工", chief: "总工 马远", keeper: "仓管 韩梅", cats: stdCats,
		items: []pItem{
			pi("raw", "CL-LFP", "磷酸铁锂 正极材料", "LFP-A2", "卷芯", "kg", 8000, 26000, "原料库 A1"),
			pi("raw", "CL-GRA", "人造石墨 负极材料", "G-450", "卷芯", "kg", 5000, 11500, "原料库 A2"),
			pi("raw", "CL-CU6", "铜箔", "6 μm", "卷芯", "kg", 1500, 4200, "原料库 A3"),
			pi("raw", "CL-AL12", "铝箔", "12 μm", "卷芯", "kg", 900, 2600, "原料库 A4"),
			pi("raw", "CL-GM", "涂覆隔膜 PE", "16 μm", "卷芯", "m²", 40000, 180000, "原料库 A5"),
			pi("raw", "CL-DJY", "电解液 LiPF6 体系", "", "注液", "kg", 2500, 7600, "原料库 A6"),
			pi("aux", "FL-SP", "导电炭黑 SP", "", "匀浆", "kg", 100, 480, "辅料库 E1"),
			pi("aux", "FL-PVDF", "PVDF 粘结剂", "", "匀浆", "kg", 200, 180, "辅料库 E2"),
			pi("aux", "FL-NMP", "NMP 溶剂", "电池级", "匀浆、涂布", "kg", 8000, 30000, "辅料库 E3"),
			pi("aux", "FL-DRJ", "导热结构胶", "双组分", "模组涂胶", "kg", 200, 560, "辅料库 E4"),
			pi("buy", "WG-AK-280", "方形铝壳", "280Ah", "电芯", "件", 8000, 21000, "外购件库 C1"),
			pi("buy", "WG-DG-280", "顶盖组件", "含极柱、防爆阀", "电芯", "件", 8000, 21500, "外购件库 C2"),
			pi("buy", "WG-LM-280", "蓝膜", "电芯绝缘膜", "电芯", "件", 8000, 23000, "外购件库 C3"),
			pi("buy", "WG-HLP", "铜铝复合汇流排", "", "模组", "件", 3000, 9000, "外购件库 C4"),
			pi("buy", "WG-DB-M", "模组端板", "铝合金", "模组", "件", 600, 1800, "外购件库 C5"),
			pi("buy", "WG-CB-M", "模组侧板", "铝合金", "模组", "件", 600, 1800, "外购件库 C6"),
			pi("buy", "WG-GD", "绝缘打包钢带", "", "模组", "件", 800, 2500, "外购件库 C7"),
			pi("buy", "WG-FPC", "采集线束 FPC", "16S", "模组", "件", 300, 900, "外购件库 C8"),
			pi("buy", "WG-JYB", "模组绝缘板", "", "模组", "件", 300, 1100, "外购件库 C9"),
			pi("std", "BZ-M5x12", "组合螺钉 M5×12", "GB/T 9074.4", "模组", "件", 6000, 30000, "标准件库 D1"),
		},
		boms: []pBOM{
			{part: "RN-JR-280", name: "280Ah 卷芯", drawing: "A", note: "正极 LFP、负极石墨，卷绕后热压", lines: []BOMLine{
				ln(Raw, "CL-LFP", "磷酸铁锂 正极材料", "", 1.98, "kg", 2.5, "匀浆"),
				ln(Raw, "CL-GRA", "人造石墨 负极材料", "", 1.02, "kg", 2.5, "匀浆"),
				ln(Aux, "FL-SP", "导电炭黑 SP", "", 0.045, "kg", 0, "匀浆"),
				ln(Aux, "FL-PVDF", "PVDF 粘结剂", "", 0.055, "kg", 0, "匀浆"),
				ln(Aux, "FL-NMP", "NMP 溶剂", "", 0.9, "kg", 15, "涂布烘干"),
				ln(Raw, "CL-CU6", "铜箔", "6 μm", 0.42, "kg", 4, "涂布"),
				ln(Raw, "CL-AL12", "铝箔", "12 μm", 0.24, "kg", 4, "涂布"),
				ln(Raw, "CL-GM", "涂覆隔膜 PE", "16 μm", 11.5, "m²", 3, "卷绕"),
			}},
			{part: "RN-LFP280", name: "280Ah 方形磷酸铁锂电芯", drawing: "B", lines: []BOMLine{
				ln(Make, "RN-JR-280", "280Ah 卷芯", "", 1, "件", 0, "入壳"),
				ln(Buy, "WG-AK-280", "方形铝壳", "280Ah", 1, "件", 0, "入壳"),
				ln(Buy, "WG-DG-280", "顶盖组件", "", 1, "件", 0, "激光焊"),
				ln(Buy, "WG-LM-280", "蓝膜", "", 1, "件", 0, "包膜"),
				ln(Raw, "CL-DJY", "电解液 LiPF6 体系", "", 0.93, "kg", 1.5, "注液"),
			}},
			{part: "RN-MOD-16S", name: "16 串电池模组", drawing: "B", lines: []BOMLine{
				ln(Make, "RN-LFP280", "280Ah 方形磷酸铁锂电芯", "", 16, "件", 0, "模组堆叠"),
				ln(Buy, "WG-HLP", "铜铝复合汇流排", "", 17, "件", 0, "激光焊"),
				ln(Buy, "WG-DB-M", "模组端板", "", 2, "件", 0, "模组堆叠"),
				ln(Buy, "WG-CB-M", "模组侧板", "", 2, "件", 0, "模组堆叠"),
				ln(Buy, "WG-GD", "绝缘打包钢带", "", 2, "件", 0, "捆扎"),
				ln(Buy, "WG-FPC", "采集线束 FPC", "16S", 1, "件", 0, "装线束"),
				ln(Buy, "WG-JYB", "模组绝缘板", "", 1, "件", 0, "装线束"),
				ln(Aux, "FL-DRJ", "导热结构胶", "", 0.35, "kg", 5, "涂胶"),
				ln(Std, "BZ-M5x12", "组合螺钉 M5×12", "GB/T 9074.4", 24, "件", 0, "紧固"),
			}},
		},
		changes: []pChange{
			{part: "RN-LFP280", why: "注液工艺优化，电解液由 0.93 kg 减到 0.90 kg", drawing: "C", note: "工艺 C 版：注液量下调",
				edit: func(b *BOM) { setQty(b, "CL-DJY", 0.90) }},
			{part: "RN-MOD-16S", why: "导热胶涂胶线型改为双线，单只模组用胶增加", edit: func(b *BOM) { setQty(b, "FL-DRJ", 0.42) }},
			{part: "RN-JR-280", why: "负极压实密度提高，石墨用量上调，待循环测试通过后生效", keep: true, edit: func(b *BOM) { setQty(b, "CL-GRA", 1.06) }},
		},
	},

	"yuanlan": {
		eng: "技术部 唐工", chief: "总工 方正", keeper: "仓管 邹宏", cats: stdCats,
		items: []pItem{
			pi("raw", "CL-DC04", "冷轧钢板 DC04", "0.7–1.2 mm", "白车身", "kg", 30000, 160000, "原料库 A1"),
			pi("raw", "CL-HC340", "高强钢板 HC340LA", "1.0–1.8 mm", "白车身", "kg", 20000, 90000, "原料库 A2"),
			pi("aux", "FL-JG", "车身结构胶", "单组分环氧", "焊装", "kg", 800, 2900, "辅料库 E1"),
			pi("aux", "FL-HS", "焊丝 ER50-6", "φ1.0", "焊装", "kg", 300, 900, "辅料库 E2"),
			pi("aux", "FL-DYQ", "电泳漆", "", "涂装", "kg", 1500, 5200, "辅料库 E3"),
			pi("aux", "FL-ZT", "中涂漆", "", "涂装", "kg", 1000, 3800, "辅料库 E4"),
			pi("aux", "FL-SQ", "面漆 珍珠白", "", "涂装", "kg", 1500, 4600, "辅料库 E5"),
			pi("aux", "FL-QQ", "清漆", "", "涂装", "kg", 1200, 4100, "辅料库 E6"),
			pi("aux", "FL-FDY", "防冻液", "-35 ℃", "总装加注", "L", 3000, 9500, "油液库 F1"),
			pi("aux", "FL-ZDY", "制动液", "DOT4", "总装加注", "L", 600, 2100, "油液库 F2"),
			pi("aux", "FL-JY", "发动机机油 0W-20", "", "总装加注", "L", 3000, 11000, "油液库 F3"),
			pi("aux", "FL-BLS", "玻璃水", "", "总装加注", "L", 2000, 7400, "油液库 F4"),
			pi("buy", "WG-FDJ-20T", "2.0T 发动机总成", "", "整车", "件", 150, 380, "总成库 G1"),
			pi("buy", "WG-BSX-8AT", "8AT 变速箱总成", "", "整车", "件", 150, 130, "总成库 G2"),
			pi("buy", "WG-QQ", "前桥总成", "", "整车", "件", 150, 410, "总成库 G3"),
			pi("buy", "WG-HQ", "后桥总成", "", "整车", "件", 150, 420, "总成库 G4"),
			pi("buy", "WG-PQ", "排气系统", "", "整车", "件", 200, 520, "总成库 G5"),
			pi("buy", "WG-YX", "燃油箱总成", "", "整车", "件", 200, 560, "总成库 G6"),
			pi("buy", "WG-YBT", "仪表板总成", "", "整车", "件", 150, 340, "总成库 G7"),
			pi("buy", "WG-ZYQ", "前排座椅总成", "", "整车", "件", 300, 760, "总成库 G8"),
			pi("buy", "WG-ZYH", "后排座椅总成", "", "整车", "件", 150, 380, "总成库 G9"),
			pi("buy", "WG-DB", "顶棚总成", "", "整车", "件", 150, 380, "总成库 G10"),
			pi("buy", "WG-XL", "整车线束", "", "整车", "件", 150, 400, "总成库 G11"),
			pi("buy", "WG-BXG-Q", "前保险杠总成", "", "整车", "件", 150, 360, "总成库 G12"),
			pi("buy", "WG-BXG-H", "后保险杠总成", "", "整车", "件", 150, 350, "总成库 G13"),
			pi("buy", "WG-CM", "车门总成（含玻璃升降）", "", "整车", "件", 600, 1500, "总成库 G14"),
			pi("buy", "WG-QDD", "前大灯总成", "LED", "整车", "件", 300, 740, "总成库 G15"),
			pi("buy", "WG-WD", "尾灯总成", "LED", "整车", "件", 300, 720, "总成库 G16"),
			pi("buy", "WG-BL-Q", "前挡风玻璃", "", "整车", "件", 150, 350, "总成库 G17"),
			pi("buy", "WG-BL-H", "后挡风玻璃", "", "整车", "件", 150, 340, "总成库 G18"),
			pi("buy", "WG-LT", "轮胎", "235/60 R18", "整车", "件", 600, 1800, "轮胎库 H1"),
			pi("buy", "WG-LG", "铝合金轮毂", "18 寸", "整车", "件", 600, 1750, "轮胎库 H2"),
			pi("buy", "WG-XDC", "蓄电池", "12 V 60 Ah", "整车", "件", 200, 560, "总成库 G19"),
			pi("std", "BZ-LS-M12", "轮毂螺栓", "M12×1.5", "整车", "件", 3000, 9000, "标准件库 D1"),
		},
		boms: []pBOM{
			{part: "YL-BIW-X7", name: "X7 白车身", drawing: "A", note: "冲压、焊装、涂胶后送涂装", lines: []BOMLine{
				ln(Raw, "CL-DC04", "冷轧钢板 DC04", "", 280, "kg", 30, "冲压"),
				ln(Raw, "CL-HC340", "高强钢板 HC340LA", "", 105, "kg", 25, "冲压"),
				ln(Aux, "FL-JG", "车身结构胶", "", 6.5, "kg", 3, "涂胶"),
				ln(Aux, "FL-HS", "焊丝 ER50-6", "", 1.2, "kg", 0, "焊装"),
			}},
			{part: "YL-X7-20T", name: "X7 2.0T 整车", drawing: "B", lines: []BOMLine{
				ln(Make, "YL-BIW-X7", "X7 白车身", "", 1, "件", 0, "焊装"),
				ln(Buy, "WG-CM", "车门总成（含玻璃升降）", "", 4, "件", 0, "总装"),
				ln(Aux, "FL-DYQ", "电泳漆", "", 3.2, "kg", 6, "电泳"),
				ln(Aux, "FL-ZT", "中涂漆", "", 2.5, "kg", 8, "中涂"),
				ln(Aux, "FL-SQ", "面漆 珍珠白", "", 3.4, "kg", 10, "面漆"),
				ln(Aux, "FL-QQ", "清漆", "", 2.6, "kg", 8, "清漆"),
				ln(Buy, "WG-FDJ-20T", "2.0T 发动机总成", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-BSX-8AT", "8AT 变速箱总成", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-QQ", "前桥总成", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-HQ", "后桥总成", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-PQ", "排气系统", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-YX", "燃油箱总成", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-YBT", "仪表板总成", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-ZYQ", "前排座椅总成", "", 2, "件", 0, "总装"),
				ln(Buy, "WG-ZYH", "后排座椅总成", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-DB", "顶棚总成", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-XL", "整车线束", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-BXG-Q", "前保险杠总成", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-BXG-H", "后保险杠总成", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-QDD", "前大灯总成", "", 2, "件", 0, "总装"),
				ln(Buy, "WG-WD", "尾灯总成", "", 2, "件", 0, "总装"),
				ln(Buy, "WG-BL-Q", "前挡风玻璃", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-BL-H", "后挡风玻璃", "", 1, "件", 0, "总装"),
				ln(Buy, "WG-LT", "轮胎", "235/60 R18", 5, "件", 0, "总装"),
				ln(Buy, "WG-LG", "铝合金轮毂", "18 寸", 5, "件", 0, "总装"),
				ln(Buy, "WG-XDC", "蓄电池", "12 V 60 Ah", 1, "件", 0, "总装"),
				ln(Std, "BZ-LS-M12", "轮毂螺栓", "M12×1.5", 20, "件", 0, "总装"),
				ln(Aux, "FL-FDY", "防冻液", "", 7.5, "L", 0, "加注"),
				ln(Aux, "FL-ZDY", "制动液", "", 0.9, "L", 0, "加注"),
				ln(Aux, "FL-JY", "发动机机油 0W-20", "", 4.5, "L", 0, "加注"),
				ln(Aux, "FL-BLS", "玻璃水", "", 3, "L", 0, "加注"),
			}},
		},
		changes: []pChange{
			{part: "YL-X7-20T", why: "涂装线改水性免中涂工艺：取消中涂，面漆由 3.4 kg 加到 3.7 kg", drawing: "C", note: "工艺 C 版：免中涂",
				edit: func(b *BOM) { dropLine(b, "FL-ZT"); setQty(b, "FL-SQ", 3.7) }},
			{part: "YL-BIW-X7", why: "侧围加强，高强钢板加 7 kg，结构胶加到 7.2 kg", drawing: "B", note: "图纸 B 版：侧围加强梁",
				edit: func(b *BOM) { setQty(b, "CL-HC340", 112); setQty(b, "FL-JG", 7.2) }},
			{part: "YL-X7-20T", why: "玻璃水壶加大到 3.5 L，待总装验证", keep: true, edit: func(b *BOM) { setQty(b, "FL-BLS", 3.5) }},
		},
	},

	"hetian": {
		eng: "品控部 邓工", chief: "厂长 高明", keeper: "仓管 刘芳", cats: foodCats,
		items: []pItem{
			pi("raw", "SC-GJF", "高筋小麦粉", "一等粉 25 kg/袋", "吐司面团", "kg", 3000, 12500, "常温库 A1"),
			pi("raw", "SC-BSY", "白砂糖", "一级", "面团、糖浆", "kg", 1500, 4800, "常温库 A2"),
			pi("raw", "SC-HY", "黄油", "无盐 10 kg/箱", "吐司面团", "kg", 400, 360, "冷藏库 B1"),
			pi("raw", "SC-NF", "全脂奶粉", "25 kg/袋", "吐司面团", "kg", 200, 700, "常温库 A3"),
			pi("raw", "SC-JD", "鸡蛋液", "巴氏杀菌", "吐司面团", "kg", 300, 900, "冷藏库 B2"),
			pi("raw", "SC-JM", "即发干酵母", "500 g/袋", "吐司面团", "kg", 60, 220, "冷藏库 B3"),
			pi("raw", "SC-YY", "食盐", "精制盐", "面团", "kg", 100, 380, "常温库 A4"),
			pi("raw", "SC-SHUI", "纯净水（工艺用水）", "", "面团、糖浆", "kg", 0, 60000, "水处理站"),
			pi("raw", "SC-NSZ", "浓缩橙汁", "65 Brix", "橙汁糖浆", "kg", 1500, 5200, "冷冻库 C1"),
			pi("aux", "FL-GJ", "面包改良剂", "", "吐司面团", "kg", 20, 85, "常温库 A5"),
			pi("aux", "FL-NMS", "柠檬酸", "食品级", "橙汁糖浆", "kg", 40, 160, "常温库 A6"),
			pi("aux", "FL-VC", "维生素 C", "食品级", "橙汁糖浆", "kg", 10, 40, "常温库 A7"),
			pi("aux", "FL-XJ", "橙香精", "", "橙汁糖浆", "kg", 15, 62, "常温库 A8"),
			pi("pack", "BC-TSD", "吐司包装袋", "OPP 带印刷", "吐司", "件", 20000, 52000, "包材库 P1"),
			pi("pack", "BC-FKK", "铝线扣", "", "吐司", "件", 20000, 60000, "包材库 P2"),
			pi("pack", "BC-BQ-TS", "吐司标签", "不干胶", "吐司", "件", 20000, 54000, "包材库 P3"),
			pi("pack", "BC-CTN-TS", "吐司周转纸箱", "12 条装", "吐司", "件", 2000, 5200, "包材库 P4"),
			pi("pack", "BC-PET", "PET 瓶坯", "500 ml", "橙汁", "件", 30000, 90000, "包材库 P5"),
			pi("pack", "BC-GAI", "防盗瓶盖", "28 mm", "橙汁", "件", 30000, 96000, "包材库 P6"),
			pi("pack", "BC-BQ-CZ", "热缩膜标签", "", "橙汁", "件", 30000, 85000, "包材库 P7"),
			pi("pack", "BC-CTN-CZ", "橙汁纸箱", "24 瓶装", "橙汁", "件", 1500, 4200, "包材库 P8"),
		},
		boms: []pBOM{
			{part: "HT-MT-001", name: "吐司面团", unit: "kg", drawing: "A", note: "每 1 kg 面团的配方，和面后发酵 60 分钟", lines: []BOMLine{
				ln(Raw, "SC-GJF", "高筋小麦粉", "", 0.55, "kg", 1, "和面"),
				ln(Raw, "SC-SHUI", "纯净水（工艺用水）", "", 0.30, "kg", 0, "和面"),
				ln(Raw, "SC-BSY", "白砂糖", "", 0.035, "kg", 0, "和面"),
				ln(Raw, "SC-HY", "黄油", "", 0.04, "kg", 0, "和面"),
				ln(Raw, "SC-NF", "全脂奶粉", "", 0.02, "kg", 0, "和面"),
				ln(Raw, "SC-JD", "鸡蛋液", "", 0.035, "kg", 0, "和面"),
				ln(Raw, "SC-JM", "即发干酵母", "", 0.006, "kg", 0, "和面"),
				ln(Raw, "SC-YY", "食盐", "", 0.01, "kg", 0, "和面"),
				ln(Aux, "FL-GJ", "面包改良剂", "", 0.004, "kg", 0, "和面"),
			}},
			{part: "HT-TJ-001", name: "橙汁调配糖浆", unit: "L", drawing: "A", note: "每 1 L 糖浆的配方，调配后巴氏杀菌", lines: []BOMLine{
				ln(Raw, "SC-NSZ", "浓缩橙汁", "65 Brix", 0.11, "kg", 0, "调配"),
				ln(Raw, "SC-BSY", "白砂糖", "", 0.045, "kg", 0, "调配"),
				ln(Raw, "SC-SHUI", "纯净水（工艺用水）", "", 0.85, "kg", 0, "调配"),
				ln(Aux, "FL-NMS", "柠檬酸", "", 0.0012, "kg", 0, "调配"),
				ln(Aux, "FL-VC", "维生素 C", "", 0.0004, "kg", 0, "调配"),
				ln(Aux, "FL-XJ", "橙香精", "", 0.0008, "kg", 0, "调配"),
			}},
			{part: "HT-TS-450", name: "吐司面包", drawing: "B", lines: []BOMLine{
				ln(Make, "HT-MT-001", "吐司面团", "", 0.5, "kg", 3, "分割整形"),
				ln(Buy, "BC-TSD", "吐司包装袋", "", 1, "件", 0, "包装"),
				ln(Buy, "BC-FKK", "铝线扣", "", 1, "件", 0, "包装"),
				ln(Buy, "BC-BQ-TS", "吐司标签", "", 1, "件", 0, "包装"),
				ln(Buy, "BC-CTN-TS", "吐司周转纸箱", "12 条装", 0.0833, "件", 0, "装箱"),
			}},
			{part: "HT-CZ-500", name: "500 ml 橙汁饮料", drawing: "B", lines: []BOMLine{
				ln(Make, "HT-TJ-001", "橙汁调配糖浆", "", 0.5, "L", 1.5, "灌装"),
				ln(Buy, "BC-PET", "PET 瓶坯", "500 ml", 1, "件", 0.5, "吹瓶"),
				ln(Buy, "BC-GAI", "防盗瓶盖", "", 1, "件", 0, "旋盖"),
				ln(Buy, "BC-BQ-CZ", "热缩膜标签", "", 1, "件", 0, "套标"),
				ln(Buy, "BC-CTN-CZ", "橙汁纸箱", "24 瓶装", 0.0417, "件", 0, "装箱"),
			}},
		},
		changes: []pChange{
			{part: "HT-MT-001", why: "改用低糖酵母，酵母减 1 g，盐按客户口味加 1 g", edit: func(b *BOM) { setQty(b, "SC-JM", 0.005); setQty(b, "SC-YY", 0.011) }},
			{part: "HT-TJ-001", why: "新季橙汁 Brix 偏低，浓缩汁加一点，糖相应减", edit: func(b *BOM) { setQty(b, "SC-NSZ", 0.115); setQty(b, "SC-BSY", 0.04) }},
			{part: "HT-CZ-500", why: "瓶盖换新模具的 26 mm 轻量盖，待灌装线验证", keep: true, edit: func(b *BOM) {
				for i := range b.Lines {
					if b.Lines[i].Code == "BC-GAI" {
						b.Lines[i].Spec, b.Lines[i].Note = "26 mm 轻量", "供应商样盖在送检"
					}
				}
			}},
		},
	},

	"qianyu": {
		eng: "出品部 曾师傅", chief: "行政总厨 冉红军", keeper: "库管 唐秀英", cats: foodCats,
		items: []pItem{
			pi("raw", "SC-NY", "精炼牛油", "块状 10 kg/箱", "牛油锅底", "kg", 300, 1100, "冷冻库 A1"),
			pi("raw", "SC-GLJ", "二荆条干辣椒", "", "牛油锅底", "kg", 100, 420, "干货库 B1"),
			pi("raw", "SC-HJ", "汉源花椒", "", "牛油锅底", "kg", 40, 150, "干货库 B2"),
			pi("raw", "SC-DB", "郫县豆瓣", "三年陈", "牛油锅底", "kg", 150, 520, "干货库 B3"),
			pi("raw", "SC-JIANG", "老姜", "", "牛油锅底", "kg", 60, 220, "冷藏库 C1"),
			pi("raw", "SC-SUAN", "大蒜", "", "牛油锅底", "kg", 60, 210, "冷藏库 C2"),
			pi("raw", "SC-BT", "冰糖", "", "牛油锅底", "kg", 50, 180, "干货库 B4"),
			pi("raw", "SC-NR", "牛里脊", "冷鲜", "麻辣牛肉串", "kg", 200, 160, "冷藏库 C3"),
			pi("raw", "SC-LO", "莲藕", "九孔藕", "藕片串", "kg", 150, 480, "冷藏库 C4"),
			pi("raw", "SC-XR", "虾仁", "冷冻 41–50 头", "虾滑", "kg", 100, 260, "冷冻库 A2"),
			pi("raw", "SC-DF", "木薯淀粉", "", "虾滑", "kg", 30, 110, "干货库 B5"),
			pi("raw", "SC-DQ", "蛋清液", "巴氏杀菌", "虾滑", "kg", 40, 130, "冷藏库 C5"),
			pi("raw", "SC-YAN", "食盐", "", "虾滑", "kg", 20, 90, "干货库 B6"),
			pi("raw", "SC-BJ", "八角", "", "火锅香料包", "kg", 20, 70, "干货库 B7"),
			pi("raw", "SC-GG", "桂皮", "", "火锅香料包", "kg", 15, 55, "干货库 B8"),
			pi("raw", "SC-XY", "香叶", "", "火锅香料包", "kg", 5, 22, "干货库 B9"),
			pi("raw", "SC-CG", "草果", "", "火锅香料包", "kg", 10, 40, "干货库 B10"),
			pi("raw", "SC-XH", "小茴香", "", "火锅香料包", "kg", 8, 30, "干货库 B11"),
			pi("raw", "SC-DX", "丁香", "", "火锅香料包", "kg", 3, 12, "干货库 B12"),
			pi("raw", "SC-SR", "砂仁", "", "火锅香料包", "kg", 5, 18, "干货库 B13"),
			pi("raw", "SC-BK", "白蔻", "", "火锅香料包", "kg", 5, 18, "干货库 B14"),
			pi("raw", "SC-SN", "山奈", "", "火锅香料包", "kg", 5, 20, "干货库 B15"),
			pi("aux", "FL-YL", "麻辣腌料", "自配", "麻辣牛肉串", "kg", 80, 260, "干货库 D1"),
			pi("aux", "FL-JY", "椒盐粉", "", "藕片串", "kg", 15, 60, "干货库 D2"),
			pi("aux", "FL-LJ", "料酒", "", "虾滑", "kg", 20, 75, "干货库 D3"),
			pi("pack", "BC-ZQ", "竹签", "20 cm", "串", "件", 8000, 26000, "包材库 P1"),
			pi("pack", "BC-ZKD", "真空袋", "20×30", "串", "件", 4000, 12000, "包材库 P2"),
			pi("pack", "BC-ZKD-L", "锅底真空袋", "25×40", "牛油锅底", "件", 2000, 6200, "包材库 P3"),
			pi("pack", "BC-HE", "虾滑保鲜盒", "250 g", "虾滑", "件", 3000, 7800, "包材库 P4"),
			pi("pack", "BC-MM", "盒盖膜", "", "虾滑", "件", 3000, 8000, "包材库 P5"),
		},
		boms: []pBOM{
			{part: "GD-XLB-01", name: "火锅香料包", unit: "kg", drawing: "A", note: "每 1 kg 香料包的配比，粗粉后分装", lines: []BOMLine{
				ln(Raw, "SC-BJ", "八角", "", 0.25, "kg", 0, "配料"),
				ln(Raw, "SC-GG", "桂皮", "", 0.18, "kg", 0, "配料"),
				ln(Raw, "SC-XY", "香叶", "", 0.10, "kg", 0, "配料"),
				ln(Raw, "SC-CG", "草果", "", 0.15, "kg", 0, "配料"),
				ln(Raw, "SC-XH", "小茴香", "", 0.12, "kg", 0, "配料"),
				ln(Raw, "SC-DX", "丁香", "", 0.05, "kg", 0, "配料"),
				ln(Raw, "SC-SR", "砂仁", "", 0.05, "kg", 0, "配料"),
				ln(Raw, "SC-BK", "白蔻", "", 0.05, "kg", 0, "配料"),
				ln(Raw, "SC-SN", "山奈", "", 0.05, "kg", 0, "配料"),
			}},
			{part: "GD-NY-1500", name: "牛油锅底（1.5 kg/块）", drawing: "B", lines: []BOMLine{
				ln(Raw, "SC-NY", "精炼牛油", "", 0.95, "kg", 3, "炒制"),
				ln(Raw, "SC-GLJ", "二荆条干辣椒", "", 0.12, "kg", 2, "炒制"),
				ln(Raw, "SC-HJ", "汉源花椒", "", 0.04, "kg", 0, "炒制"),
				ln(Raw, "SC-DB", "郫县豆瓣", "", 0.20, "kg", 2, "炒制"),
				ln(Raw, "SC-JIANG", "老姜", "", 0.06, "kg", 8, "炒制"),
				ln(Raw, "SC-SUAN", "大蒜", "", 0.05, "kg", 8, "炒制"),
				ln(Raw, "SC-BT", "冰糖", "", 0.03, "kg", 0, "炒制"),
				ln(Make, "GD-XLB-01", "火锅香料包", "", 0.02, "kg", 0, "炒制"),
				ln(Buy, "BC-ZKD-L", "锅底真空袋", "", 1, "件", 0, "真空包装"),
			}},
			{part: "CC-NR-10", name: "麻辣牛肉串（10 签/份）", drawing: "A", lines: []BOMLine{
				ln(Raw, "SC-NR", "牛里脊", "", 0.42, "kg", 3, "改刀"),
				ln(Aux, "FL-YL", "麻辣腌料", "", 0.05, "kg", 0, "腌制"),
				ln(Buy, "BC-ZQ", "竹签", "20 cm", 10, "件", 2, "穿串"),
				ln(Buy, "BC-ZKD", "真空袋", "", 1, "件", 0, "真空包装"),
			}},
			{part: "CC-OP-10", name: "藕片串（10 签/份）", drawing: "A", lines: []BOMLine{
				ln(Raw, "SC-LO", "莲藕", "", 0.55, "kg", 18, "去皮切片"),
				ln(Aux, "FL-JY", "椒盐粉", "", 0.01, "kg", 0, "调味"),
				ln(Buy, "BC-ZQ", "竹签", "20 cm", 10, "件", 2, "穿串"),
				ln(Buy, "BC-ZKD", "真空袋", "", 1, "件", 0, "真空包装"),
			}},
			{part: "WH-XH-250", name: "虾滑（250 g/盒）", drawing: "A", lines: []BOMLine{
				ln(Raw, "SC-XR", "虾仁", "", 0.17, "kg", 2, "打浆"),
				ln(Raw, "SC-DF", "木薯淀粉", "", 0.025, "kg", 0, "打浆"),
				ln(Raw, "SC-DQ", "蛋清液", "", 0.03, "kg", 0, "打浆"),
				ln(Raw, "SC-YAN", "食盐", "", 0.004, "kg", 0, "打浆"),
				ln(Aux, "FL-LJ", "料酒", "", 0.006, "kg", 0, "打浆"),
				ln(Buy, "BC-HE", "虾滑保鲜盒", "250 g", 1, "件", 0, "装盒"),
				ln(Buy, "BC-MM", "盒盖膜", "", 1, "件", 0, "封膜"),
			}},
		},
		changes: []pChange{
			{part: "CC-OP-10", why: "改用去皮机，莲藕去皮损耗由 18% 降到 14%", edit: func(b *BOM) { setScrap(b, "SC-LO", 14) }},
			{part: "GD-NY-1500", why: "口味调整：少辣多麻，干辣椒减 20 g，花椒加 5 g", edit: func(b *BOM) { setQty(b, "SC-GLJ", 0.10); setQty(b, "SC-HJ", 0.045) }},
			{part: "WH-XH-250", why: "每盒虾仁加 10 g，待试吃通过后生效", keep: true, edit: func(b *BOM) { setQty(b, "SC-XR", 0.18) }},
		},
	},
}

// seedPack gives a demo company other than the forging one its stores and BOMs, once.
func (s *Service) seedPack(company string) error {
	p, ok := packs[company]
	if !ok || s.Products == nil {
		return nil
	}
	refs := s.Products(company)
	has := false
	for _, r := range refs {
		if slices.ContainsFunc(p.boms, func(b pBOM) bool { return b.part == r.Part }) {
			has = true
		}
	}
	if !has {
		return nil // the config makes none of the parts the BOMs are for
	}
	mark := company + "/bom"
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM seeded WHERE company = ?`, mark).Scan(&n)
	if n > 0 {
		return nil
	}
	// once: a seed that fails half way is not tried again on top of what it wrote
	if _, err := s.db.Exec(`INSERT INTO seeded (company) VALUES (?)`, mark); err != nil {
		return err
	}
	now := s.Now
	defer func() { s.Now = now }()
	back := func(days float64) func() time.Time {
		at := time.Now().Add(-time.Duration(days * 24 * float64(time.Hour)))
		return func() time.Time { return at }
	}
	for _, c := range p.cats {
		if _, ok := s.cat(company, c.Key); ok {
			continue
		}
		if _, err := s.SaveCat(company, c); err != nil {
			return fmt.Errorf("%s: %w", c.Label, err)
		}
	}
	s.Now = back(45)
	for _, it := range p.items {
		spec := map[string]string{}
		if it.spec != "" {
			spec["规格"] = it.spec
		}
		if it.use != "" {
			spec["用在"] = it.use
		}
		saved, err := s.SaveItem(company, Item{Cat: it.cat, Code: it.code, Name: it.name, Attrs: spec, Unit: it.unit, Safety: it.safety, Place: it.place})
		if err != nil {
			return fmt.Errorf("%s: %w", it.code, err)
		}
		if it.stock > 0 {
			if _, _, err := s.Record(company, p.keeper, MoveInput{Item: saved.ID, Kind: In, Qty: it.stock, Ref: "采购入库"}); err != nil {
				return fmt.Errorf("%s: %w", it.code, err)
			}
		}
	}
	for i, b := range p.boms {
		s.Now = back(40 - float64(i))
		if b.unit == "" {
			b.unit = "件"
		}
		saved, err := s.SaveBOM(company, p.eng, BOM{Part: b.part, Name: b.name, Drawing: b.drawing, Unit: b.unit, Note: b.note, Lines: slices.Clone(b.lines)})
		if err != nil {
			return fmt.Errorf("%s: %w", b.part, err)
		}
		if _, _, err := s.ReleaseBOM(company, p.chief, saved.ID, Release{Why: "首次发布", Effective: s.Now().In(cst).Format("2006-01-02")}); err != nil {
			return fmt.Errorf("%s: %w", b.part, err)
		}
	}
	for i, c := range p.changes {
		s.Now = back(10 - float64(i))
		cur, err := s.BOMOf(company, c.part)
		if err != nil {
			return fmt.Errorf("%s: %w", c.part, err)
		}
		next, err := s.CopyBOM(company, p.eng, cur.ID)
		if err != nil {
			return fmt.Errorf("%s: %w", c.part, err)
		}
		c.edit(next)
		if c.drawing != "" {
			next.Drawing = c.drawing
		}
		if c.note != "" {
			next.Note = c.note
		}
		saved, err := s.SaveBOM(company, p.eng, *next)
		if err != nil {
			return fmt.Errorf("%s: %w", c.part, err)
		}
		if c.keep {
			continue
		}
		if _, _, err := s.ReleaseBOM(company, p.chief, saved.ID, Release{Why: c.why, Effective: s.Now().In(cst).Format("2006-01-02"), Drawing: c.drawing}); err != nil {
			return fmt.Errorf("%s: %w", c.part, err)
		}
	}
	return nil
}
