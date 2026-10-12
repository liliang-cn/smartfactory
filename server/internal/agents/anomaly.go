package agents

import (
	"context"

	"smartfactory/internal/anomaly"
	"smartfactory/internal/auth"
)

// Hints answers for a company's readings unusual for their machine, as the person asking may see
// them (the console's own operation, ops.Anomalies).
type Hints func(who auth.Who, company string) (*anomaly.Report, error)

// anomalyTool finds the company's machines that read unusually for themselves: what an engineer
// would look at before writing up a problem.
func anomalyTool(hints Hints, company string) tool {
	return tool{name: "machine_anomalies",
		desc:   "查接了真实设备的机器读数异常：哪台设备的哪个读数（温度、功率、振动…）和它自己在同一状态下的平常值不一样，偏离几个标准差（z），平常值（均值±标准差），从什么时候起；现在的和最近 30 天的。只是提示，设备照常运行；仿真的设备没有。",
		params: obj(map[string]any{"asset": prop("string", "设备编号，如 HP-101（可省：全部）")}),
		run: func(ctx context.Context, a args) (any, error) {
			r, err := hints(caller(ctx), company)
			if err != nil {
				return nil, err
			}
			asset := a.str("asset")
			now, recent := []anomaly.Anomaly{}, []anomaly.Anomaly{}
			for _, x := range r.Current {
				if asset == "" || x.Asset == asset {
					now = append(now, x)
				}
			}
			for _, x := range r.Recent {
				if (asset == "" || x.Asset == asset) && x.Ended != nil && len(recent) < 20 {
					recent = append(recent, x)
				}
			}
			out := map[string]any{"current": now, "ended": recent}
			if asset != "" {
				for _, m := range r.Machines {
					if m.Asset == asset {
						out["normals"] = m.Fields
					}
				}
			}
			if len(now) == 0 && len(recent) == 0 {
				out["note"] = "没有读数异常（或者这些设备还没接真实数据、还在学习平常值）"
			}
			return out, nil
		}}
}
