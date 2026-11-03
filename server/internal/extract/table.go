package extract

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Table reads a spreadsheet as rows of cells, for an import: an Excel workbook's first sheet with
// anything in it (the values as the sheet shows them), or a CSV in UTF-8 or GBK, its separator
// a comma, a semicolon or a tab.
func Table(name string, data []byte) (rows [][]string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("文件读不出来（可能已损坏）：%v", r)
		}
	}()
	switch ext := strings.ToLower(filepath.Ext(name)); ext {
	case ".xlsx", ".xlsm":
		f, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("不是有效的 .xlsx 文件：%w", err)
		}
		defer f.Close()
		for _, sheet := range f.GetSheetList() {
			rows, err := f.GetRows(sheet)
			if err != nil {
				continue
			}
			for _, r := range rows {
				if filled(r) > 0 {
					return rows, nil
				}
			}
		}
		return nil, fmt.Errorf("工作簿是空的")
	case ".csv", ".txt", ".tsv":
		text, _ := Decode(data)
		sep := ','
		first, _, _ := strings.Cut(text, "\n")
		for _, c := range []rune{'\t', ';'} {
			if strings.Count(first, string(c)) > strings.Count(first, string(sep)) {
				sep = c
			}
		}
		r := csv.NewReader(strings.NewReader(text))
		r.Comma, r.FieldsPerRecord, r.LazyQuotes = sep, -1, true
		rows, err := r.ReadAll()
		if err != nil && len(rows) == 0 {
			return nil, fmt.Errorf("CSV 读不出来：%w", err)
		}
		return rows, nil
	case ".xls":
		return nil, fmt.Errorf("%w：.xls 是旧版 Excel 格式，另存为 .xlsx 再导入", ErrUnsupported)
	default:
		return nil, fmt.Errorf("%w：导入要用 Excel（.xlsx）或 CSV", ErrUnsupported)
	}
}
