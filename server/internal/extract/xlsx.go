package extract

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// readXLSX writes each sheet as a section, each row a line of "header=value" pairs with the
// values as the sheet shows them (dates and numbers formatted).
func readXLSX(data []byte) (Doc, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "encrypt") || strings.Contains(err.Error(), "password") {
			return Doc{}, fmt.Errorf("工作簿加了密码，去掉密码再上传")
		}
		return Doc{}, fmt.Errorf("不是有效的 .xlsx 文件：%w", err)
	}
	defer f.Close()
	d := Doc{Kind: "xlsx"}
	var b strings.Builder
	left := MaxRows
	for _, sheet := range f.GetSheetList() {
		if left <= 0 {
			d.Problems = append(d.Problems, fmt.Sprintf("只取了前 %d 行", MaxRows))
			break
		}
		table, err := f.GetRows(sheet)
		if err != nil {
			d.Problems = append(d.Problems, fmt.Sprintf("工作表「%s」读不出来：%v", sheet, err))
			continue
		}
		text, n := rows(table, left)
		if strings.TrimSpace(text) == "" {
			continue
		}
		left -= n
		fmt.Fprintf(&b, "# %s\n\n%s\n", sheet, text)
	}
	d.Body = b.String()
	if strings.TrimSpace(d.Body) == "" {
		d.Problems = append(d.Problems, "工作簿是空的")
	}
	return d, nil
}
