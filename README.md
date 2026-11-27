# NL&TKTT: gofl, định vị lỗi cho dự án Go

Đồ án tái hiện hai bài báo về định vị lỗi (fault localization) cho Go, gói trong công cụ dòng lệnh `gofl` và một extension VS Code.

| Nhóm | Lệnh | Bài báo | Ý chính |
|---|---|---|---|
| C | `gofl crash` | Crash report mining + LLM | Đọc stack trace, gom nhóm, xếp hạng file/hàm nghi ngờ, tuỳ chọn nhờ LLM |
| E | `gofl perf` | EffiHolmes | So sánh profile trước/sau để tìm chỗ gây chậm |

## Cấu trúc thư mục (phần được push)

- `plan_crash/` — kế hoạch (`KE_HOACH_BAI_C.md`) và mã nguồn `crash_repro` cho bài C.
- `plan_efficency/gofl-run/` — mã nguồn, runbook và báo cáo cho bài E.
- `extension/` — extension VS Code: sinh prompt cho Agent từ crash log/perf và tô màu trên file (cam: gofl chỉ ra, xanh dương: Agent nói lỗi, xanh lá/đỏ: Agent thêm/xoá). Xem `extension/README.md`.
- `references/` — các bài báo tham khảo.
- `papers/` — tài liệu báo cáo hằng tuần (`Week1.pdf`, `Week2.pdf`).

## Bắt đầu nhanh

```
cd plan_crash/crash_repro
go build -o gofl.exe ./cmd/gofl
gofl crash -h
```

Cài extension và cách cấu hình `gofl.path`: xem `extension/README.md`.

## Ghi chú

File build (`*.exe`, `*.zip`, `*.vsix`) và `node_modules/` không được theo dõi bởi git.
