# Báo cáo tái hiện bài C (`gofl crash`)

Module Go độc lập (`module gofl`, go 1.23). Chạy lệnh trong thư mục này:

```sh
go vet ./... && go test ./...
go build -o gofl.exe ./cmd/gofl && go build -o goflbench.exe ./cmd/goflbench
./goflbench.exe -dir testdata/goflsample
./goflbench.exe -dir testdata/goflrealworld -layer realworld
```

Kết quả đã chạy nằm trong `results/` (cả `.txt` lẫn `.json`).

## Đối chiếu 7 bước với code

| Bước | Code | Trạng thái |
|---|---|---|
| 1. Thu crash | `internal/runner` (chạy `go test -json`, timeout, `Unrunnable`), `internal/logprofile` (cắt khối panic từ log), `cmd/gofl/crash.go` (stdin, `-log`, chạy test) | Xong, test pass |
| 2. Parse và chuẩn hóa | `internal/crashsig`: `Parse`, `Relativize`, `Normalize`, `Hash`, `Subsumes` | Xong. Bản này vá 2 chỗ hở, xem bên dưới |
| 3. Gom nhóm 4 mức | `internal/crashrank` (`GroupEvents`) | Xong, test pass |
| 4. Xếp hạng, gợi ý hàm | `internal/crashrank` (`Rank`), `internal/crashflow` | Xong, test pass |
| 5. LLM triage (mặc định tắt) | `internal/llm` (prompt, parse, redact, vote 5 lần, eval) | Xong, test bằng provider giả |
| 6. Xuất kết quả | `internal/report` (md, json, sarif) | Xong, test pass |
| 7. Kiểm chứng | `internal/bench`, `cmd/goflbench` | Đã chạy cấu hình không LLM, xem bảng dưới |

## Bước 2: phần làm thêm trong lần này

Phần TV2 tuần 1 (`C_week1_crashsig_TV2`) được đưa vào làm bộ kiểm tra cho `crashsig` của gofl. Bộ này gồm 13 stack C1–C13 (C9 có 3 biến thể), một stack `debug.Stack()` và 9 stack thật W1–W9. Chạy trên code gofl, bộ kiểm tra lộ ra hai chỗ hở và cả hai đã được vá trong `internal/crashsig/crashsig.go`:

1. **Stack từ `runtime/debug.Stack()` không có dòng `panic:`.** Trước đây `Parse` trả lỗi `ErrNoPanic`. Giờ nó lấy goroutine đầu tiên và để `Exception` rỗng. Input không phải stack (không có header goroutine) vẫn trả lỗi.
2. **Dòng log có tiền tố thời gian.** Dòng `2026/11/13 15:05:59 http: panic serving [::1]:49951: ...` không được nhận ra vì regex neo ở đầu dòng. Giờ regex chấp nhận tiền tố, nên cả 142 panic `http: panic serving` trong `server_crash.log` đều parse được.
3. **(vá ngày 17/11, `internal/logprofile/default.go`) Panic goroutine cuối log bị nuốt.** Panic W6 (`service/comment.go:126`) đứng ngay sau stack của panic HTTP cuối, không có dòng log nào ở giữa. Profile mặc định coi dòng `panic:` đó là panic lồng nhau và nối vào khối trước, nên `gofl crash -log server_crash.log` chỉ ra 142 sự kiện thay vì 143 và mất ca W6. Giờ một dòng `panic:`/`fatal error:` xuất hiện sau header `goroutine` sẽ mở khối mới (panic lồng nhau thật luôn in trước header goroutine). Test hồi quy: `TestServerLogKeepsFinalGoroutinePanic`.

Ngoài ra, bỏ frame cơ chế panic giờ cắt mọi frame phía trên frame `panic` cuối cùng, tức các hàm xử lý `recover` như `runtime/debug.Stack` hay `net/http.(*conn).serve.func1`. Trước đây chỉ bỏ các frame runtime nằm ở đầu.

Test thêm:
- `internal/crashsig/step2_parse_test.go`: crash point (hàm, file, dòng) và Exception của C1–C13, W1–W9, debugstack. Có thêm W8 (4 stack, cùng file crash) và toàn bộ panic trong log server.
- `internal/crashsig/golden_test.go`: golden file chữ ký đã Normalize kèm Hash cho 16 stack, nằm ở `testdata/golden/`. Cập nhật bằng `go test ./internal/crashsig -update`.
- `internal/crashsig/testdata/`: các stack C1–C13 do TV2 thu, dùng placeholder `<root>` và `<goroot>`.

Khác biệt thiết kế đã giữ nguyên: gofl bỏ tiền tố `fatal error: ` khỏi `Exception` và ghi vào cờ `Fatal` (bản TV2 giữ cả tiền tố). Test được chỉnh theo cách của gofl.

## Bước 7: kết quả (không LLM)

Pipeline chạy hết với 22/22 ca.

**Lớp 1, lỗi cài sẵn trong `goflsample` (13 ca, chỉ dùng khi phát triển):**

| Cấu hình | file@1 | file@3 | func@1 | func@3 | line@1 |
|---|---:|---:|---:|---:|---:|
| gofl crash | 100% | 100% | 77% (10/13) | 85% (11/13) | 77% |
| baseline: file của frame user đầu tiên | 100% | 100% | 77% | 77% | 77% |

**Lớp 2, `goflrealworld` (9 ca thật W1–W9):**

| Cấu hình | file@1 | file@3 | file@5 | func@1 | func@3 | line@1 |
|---|---:|---:|---:|---:|---:|---:|
| gofl crash | 67% (6/9) | 78% (7/9) | 78% | 56% (5/9) | 67% (6/9) | 44% (4/9) |
| baseline | 67% (6/9) | 67% (6/9) | 67% | 56% | 56% | 44% |

So với `docs/THRESHOLDS.md` (lớp 2):
- **file@3 ≥ 60%:** đạt (78%).
- **file@3 hơn baseline ≥ 10 điểm:** đạt (+11 điểm, tức đúng 1 ca trên 9).
- **Tỉ lệ pipeline ≥ 70%:** đạt (100%).

## Câu hỏi mở C15: xếp hạng tệp có tốt hơn đọc frame đầu không?

Trên lớp 1, không: kết quả bằng baseline (+0 điểm), vì mỗi lỗi chỉ có một stack nên thứ tự xếp hạng trùng thứ tự frame. Trên lớp 2, có, nhưng chỉ nhờ **một ca** (+11 điểm). Với 9 ca, một ca bằng 11 điểm, nên đây là bằng chứng sơ bộ và không đủ để kết luận. Phần xếp hạng chỉ có ích khi nhiều crash gom được vào một nhóm (W2, W8 có 2 và 4 sự kiện).

## Giai đoạn 2: trạng thái (2026-11-24)

Giai đoạn 2 thay bước "LLM viết patch" bằng Agent sửa lỗi (xem `../KE_HOACH_BAI_C.md`). Đã chốt: Agent nhận **top 3 file** của `crashrank.Rank` (mỗi file tối đa 3 hàm), bỏ thí nghiệm E2, bước 8 hoãn, `crash_repro` là bản chính. Đã làm bước 10:

- `internal/agentprompt`: prompt tất định, hai chế độ dùng chung một template. E0 chỉ có crash và stack, E1 thêm top 3 file. Chênh lệch E1 − E0 đo đúng giá trị của top 3.
- `gofl crash -agent-prompt` in prompt E1; `goflbench -agent-prompt-dir` ghi prompt E0/E1 của mọi ca (`results/prompts/`).
- `bench.AgentWorkspace`: bản sao dự án cho Agent, không có `bugs.json` và `// BUG:`.
- Chưa chạy Agent và chưa có số E0/E1. Số Acc@k không LLM không đổi.

## Giới hạn

- **Chưa đo cấu hình có LLM thật.** Không có backend LLM nào trong môi trường này. Đường đo đã được kiểm bằng `-llm fake`: kết quả trùng baseline đúng như thiết kế, và các tag `// BUG:` đã được xoá khỏi prompt. Muốn đo thật: đặt `GOFL_LLM_*` (hoặc `GOFL_LLM_PROVIDER=anthropic` cùng `ANTHROPIC_API_KEY`) rồi chạy `goflbench -dir testdata/goflsample -llm local -configs all`. Theo `docs/THRESHOLDS.md`, LLM không có ngưỡng go/no-go.
- Lớp 2 chỉ có 9 ca, chưa tới khoảng 10–20 ca mà ngưỡng yêu cầu.
- `testdata/goflsample/perf.test.exe` không được copy sang đây vì là file nhị phân.
