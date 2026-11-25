package report

import (
	"fmt"
	"html"
	"strings"
)

var tableEscapes = strings.NewReplacer(
	"\\", "\\\\", "|", "&#124;", "`", "&#96;", "*", "&#42;",
	"_", "&#95;", "[", "&#91;", "]", "&#93;", "~", "&#126;",
)

func tableCell(value string) string {
	return tableEscapes.Replace(html.EscapeString(strings.Join(strings.Fields(value), " ")))
}

func markdown(doc document) []byte {
	var out strings.Builder
	out.WriteString("# Báo cáo hiệu năng Go\n\n")
	fmt.Fprintf(&out, "| Phiên bản công cụ | Trạng thái | Chế độ | Go | Hệ điều hành | Thời gian (ms) | Băm tên |\n")
	out.WriteString("|---|---|---|---|---|---:|---|\n")
	namesHashed := "Không"
	if doc.NamesHashed {
		namesHashed = "Có"
	}
	fmt.Fprintf(&out, "| %s | %s | %s | %s | %s | %d | %s |\n\n",
		tableCell(doc.Run.ToolVersion), tableCell(doc.Run.Status), tableCell(doc.Run.Mode),
		tableCell(doc.Run.GoVersion), tableCell(doc.Run.OS), doc.Run.DurationMS, namesHashed)
	if doc.Run.ErrorCode != nil {
		fmt.Fprintf(&out, "Mã lỗi %s.\n\n", tableCell(*doc.Run.ErrorCode))
	}

	out.WriteString("## Hotspot\n\n")
	if len(doc.Hotspots) == 0 {
		out.WriteString("Không có hotspot trong đầu vào.\n\n")
	} else {
		if perOp(doc.Hotspots) {
			out.WriteString("Xếp hạng theo thời gian mỗi thao tác của benchmark (giây mỗi lần chạy), không theo giây thô của profile.\n\n")
			out.WriteString("| Hạng | Hàm | Tệp | Mỗi thao tác, tải nhỏ (s) | Mỗi thao tác, tải lớn (s) | Δ mỗi thao tác (s) | NDC mỗi thao tác |\n")
			out.WriteString("|---:|---|---|---:|---:|---:|---:|\n")
			for _, h := range doc.Hotspots {
				fmt.Fprintf(&out, "| %d | %s | %s | %.6g | %.6g | %.6g | %.2f%% |\n",
					h.Rank, tableCell(h.Function), tableCell(h.File),
					val(h.BaselinePerOpSeconds), val(h.ScaledPerOpSeconds), val(h.DeltaPerOpSeconds), 100*val(h.NDCPerOp))
			}
		} else {
			out.WriteString("| Hạng | Hàm | Tệp | Self-time tải nhỏ (s) | Self-time tải lớn (s) | Δself (s) | NDC |\n")
			out.WriteString("|---:|---|---|---:|---:|---:|---:|\n")
			for _, h := range doc.Hotspots {
				fmt.Fprintf(&out, "| %d | %s | %s | %.6g | %.6g | %.6g | %.2f%% |\n",
					h.Rank, tableCell(h.Function), tableCell(h.File),
					h.BaselineSelfSeconds, h.ScaledSelfSeconds, h.DeltaSelfSeconds, 100*h.NDC)
			}
		}
		out.WriteString("\n")
	}

	out.WriteString("## Đường gọi tới hotspot\n\n")
	switch doc.Run.PathsMethod {
	case "paper":
		out.WriteString("Phương pháp `paper` dùng tổng Δself của các khung trên đường gọi, so với trung bình ở tải nhỏ (đơn vị của cột Điểm theo cách xếp hạng hotspot ở trên).\n\n")
	case "stack":
		out.WriteString("Phương pháp `stack` dùng chênh lệch thời gian của chính stack đó giữa tải lớn và tải nhỏ (đơn vị của cột Điểm theo cách xếp hạng hotspot ở trên).\n\n")
	}
	if len(doc.CriticalPaths) == 0 {
		out.WriteString("Không có đường gọi trong đầu vào.\n")
		return []byte(out.String())
	}
	out.WriteString("| Hạng hotspot | Hàm đích | Đường gọi | Điểm (s) | Giới hạn số đường |\n")
	out.WriteString("|---:|---|---|---:|---|\n")
	for _, p := range doc.CriticalPaths {
		truncated := "Không"
		if p.Truncated {
			truncated = "Có"
		}
		fmt.Fprintf(&out, "| %d | %s | %s | %.6g | %s |\n", p.TargetRank, tableCell(p.Target),
			tableCell(p.Signature), p.Score, truncated)
	}
	return []byte(out.String())
}

// perOp reports whether every hotspot has the per-operation fields.
func perOp(hs []hotspot) bool {
	for _, h := range hs {
		if h.DeltaPerOpSeconds == nil || h.NDCPerOp == nil || h.BaselinePerOpSeconds == nil || h.ScaledPerOpSeconds == nil {
			return false
		}
	}
	return len(hs) > 0
}

func val(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
