package agents

import (
	"context"
	"time"

	"smartfactory/internal/tables"
)

// tablesTool tells which tables of a restaurant are free, today's dining figures, and how long
// one table has been taken.
func tablesTool(svc *tables.Service, company string) tool {
	return tool{name: "dining_tables",
		desc: "查餐桌：不填 table 时给出现在哪些桌空着（按门店、区域、座位数）和今天的上座率、座位利用率、翻台率、平均用餐时长；填 table（如 A03）时给出这张桌现在的状态、坐了多久、几个人、今天的用餐记录。每个数注明来源：measured 是摄像头、收银、服务员上报的，simulated 是孪生仿真的，不是真实门店。",
		params: obj(map[string]any{
			"table": prop("string", "桌号，如 A03（可省）"),
			"plant": prop("string", "门店 id（几家店都有这个桌号时要填）"),
			"seats": prop("integer", "只看至少能坐这么多人的空桌"),
		}),
		run: func(_ context.Context, a args) (any, error) {
			clock := func(ms int64) string { return time.UnixMilli(ms).In(shanghai).Format("15:04") }
			if t := a.str("table"); t != "" {
				c, err := svc.Table(company, a.str("plant"), t)
				if err != nil {
					return nil, err
				}
				out := map[string]any{"table": c.Table.ID, "shop": c.Table.PlantName, "area": c.Table.Area, "seats": c.Table.Seats,
					"state": tables.StateLabel[c.Table.State], "source": c.Table.Source, "reported": c.Table.Reported, "todaySessions": len(c.Today)}
				if c.Open != nil && c.Minutes != nil {
					out["seatedAt"], out["minutes"], out["people"] = clock(c.Open.Start), *c.Minutes, c.Open.People
				}
				return out, nil
			}
			ov, err := svc.Overview(company)
			if err != nil {
				return nil, err
			}
			free := []map[string]any{}
			for _, t := range ov.Tables {
				if t.State != tables.Free || (a.str("plant") != "" && t.Plant != a.str("plant")) || t.Seats < a.num("seats", 0) {
					continue
				}
				free = append(free, map[string]any{"table": t.ID, "shop": t.PlantName, "area": t.Area, "seats": t.Seats, "source": t.Source})
			}
			return map[string]any{"free": free, "today": ov.Plants, "total": ov.Total}, nil
		}}
}
