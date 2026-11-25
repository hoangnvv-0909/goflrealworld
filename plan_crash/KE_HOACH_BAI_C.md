# Plan: tái hiện bài C (`gofl crash`) trong 7 bước

## Context
Repo `D:\NL&TKTT\gofl` đã có khung Go (go 1.25) nhưng các hàm lõi của bài C vẫn là stub trả `ErrNotImplemented`:
`internal/crashsig`, `internal/crashrank`, `internal/llm`, `internal/logprofile`, `internal/runner`, `cmd/gofl/crash.go`.
Đã có sẵn và dùng lại được: `internal/finding` (kiểu `Finding`/`Report`), `internal/report` (ghi md/json/sarif), `cmd/gofl/main.go` (cờ chung `addCommon`),
dữ liệu `testdata/goflsample` (lỗi C1–C13 cài sẵn, đáp án ở `bugs.json`), `testdata/goflrealworld/stacks/server_crash.log`, và `internal/benchcheck`.
Mục tiêu: `gofl crash` chạy end-to-end theo 7 bước của bài (thu → parse → gom nhóm → xếp hạng → LLM tùy chọn → xuất → kiểm chứng) và có số đo Acc@k.
Ghi chú: thư mục untracked `tv3/` có bản triển khai `llm`/`logprofile` làm sẵn; xem xét chuyển vào `gofl/internal/` ở Bước 5 thay vì viết lại.

## Bước 1. Thu crash
- `internal/runner.Run`: chạy `go test -json` với `Timeout`, `Env`, `Run`; trả `Unrunnable` kèm lý do khi build lỗi/timeout.
- `internal/logprofile`: profile mặc định đọc stream (iter.Seq2), cắt khối từ `panic:` / `fatal error:` tới hết stack goroutine → `crashrank.Event`.
- `cmd/gofl/crash.go`: nguồn vào là stdin, `-log`, hoặc chạy test trực tiếp.
- Test: `testdata/goflrealworld/stacks/server_crash.log` + output thật của C1–C13.

## Bước 2. Parse và chuẩn hóa (`internal/crashsig`)
- `Parse`: tách `Exception`, danh sách `Frame{Func,File,Line}`; xử lý `...N frames elided...`, `created by`, `[recovered]`, `fatal error`.
- Bỏ frame của cơ chế panic (`runtime.gopanic`, `runtime.panic*`, `sigpanic`, `testing.tRunner`) → frame đầu là crash point; đặt `NoCrashPoint` khi không có.
- `Normalize`: bỏ địa chỉ/đối số, `+0x..`, số goroutine, hậu tố generic `[...]`, `.funcN`; đường dẫn tương đối, dấu `/`.
- `Hash` (ổn định), `Subsumes` (chạy liên tiếp theo `docs/CONTRACT.md` mục 5).
- Test table-driven bằng stack thật của C1–C13 (golden file).

## Bước 3. Gom nhóm 4 mức (`crashrank.GroupEvents`)
- L1: hash gồm số dòng; L2: hash chữ ký đã Normalize; L3: `Subsumes`; L4: cùng file crash point.
- Union-find, gộp dần L1→L4, ghi `Group.Level` là mức gộp cao nhất.
- Test: nhiều crash cùng lỗi gốc phải vào một nhóm; lỗi khác nhau không bị gộp ở L1–L3.

## Bước 4. Xếp hạng và gợi ý hàm (`crashrank.Rank`)
- Score = IAD·IBF·FF (FF = tỉ lệ trace chứa file; IBF = log(1+G/G_f); IAD = 1/(1+d̄)). Ghi công thức vào package doc vì bài không nêu.
- Gợi ý hàm theo tần suất trong stack nhóm cho top file; trả `[]finding.Finding` đã xếp hạng, có `Evidence{Kind:"stack-rank"}`.
- Test: với một crash đơn, thứ tự = thứ tự frame (ghi nhận hạn chế C15).

## Bước 5. LLM triage, mặc định tắt (`internal/llm`)
- `Provider` cho một backend (local hoặc API qua `GOFL_LLM_*`), `BuildPrompt` 5 phần theo Listing 1, mặc định dùng stack, chuyển sang message khi stack bị cắt.
- Chỉ đưa mã các hàm liên quan, cắt bằng `go/parser`/`go/ast`.
- `ParseResponse` chịu văn bản thừa và JSON bị cắt; `Redact` chạy trước khi gửi; chế độ in ra dữ liệu sẽ gửi mà không gửi.
- Mỗi ca chạy 5 lần, nhiệt độ 1,0; đúng khi ≥3/5. Test bằng `Provider` giả (không gọi mạng).

## Bước 6. Xuất kết quả
- Nối trong `cmd/gofl/crash.go`: parse → group → rank → (llm) → `report.Write` (md/json/sarif); đưa giới hạn vào `Report.Notes`.
- Kiểm tra SARIF 2.1.0 hợp lệ và JSON khớp `finding.SchemaVersion`.

## Bước 7. Kiểm chứng tái hiện
- Chạy `gofl crash` trên mỗi lỗi C1–C13 của `goflsample`; so với `bugs.json` bằng benchmark runner / `internal/benchcheck`: Acc@1/3/5 mức file và hàm.
- Hai cấu hình: không LLM và có LLM; ghi ca không chạy hết pipeline.
- Trả lời câu hỏi mở C15: xếp hạng tệp có tốt hơn đọc frame đầu không, so với ngưỡng trong `docs/THRESHOLDS.md`.

## Verification
- `cd gofl && go vet ./... && go test ./...` sau mỗi bước.
- Chạy tay: `go test ./crash -run TestNth 2>&1 | gofl crash -format json` trong `testdata/goflsample`.
- Bước 7 cho bảng Acc@k là bằng chứng cuối.

---

# Giai đoạn 2: từ tái hiện bài C sang Agent sửa lỗi (ghi ngày 2026-11-19, chưa làm)

## Bối cảnh và quyết định
- Bài C (arXiv 2609.04483): gom nhóm → xếp hạng file/hàm → LLM nhận **mã nguồn 3 file đầu** + stack (+ ranking tùy chọn) và **viết patch**; chạy 5 lần, đúng khi ≥3/5; chấm tay. Trên toàn bộ 38 bug, cấu hình tốt nhất là mã nguồn + stack trace **không kèm ranking** (định vị 71%, sửa 52%).
- Tiền đề của bài: LLM không được thấy toàn bộ codebase. Hai hạn chế bài tự nêu: không có test nên phải chấm tay; khó tách patch khỏi câu trả lời của LLM.
- Điều kiện của ta: **Agent luôn có full scope**; LLM1 (định vị kiểu bài C) dùng **API bên ngoài**.
- Quyết định: `plan_crash` giữ vai **baseline tái hiện trung thành bài C**. Phần mở rộng thay bước "LLM gợi ý patch" bằng **Agent sửa + kiểm chứng bằng test**.
- **Truyền thẳng ranking cho Agent**, không qua tầng LLM/Agent trung gian để "định vị rồi viết prompt". Lý do: tầng trung gian thấy ít hơn hoặc bằng Agent; mất lập luận khi bàn giao; lỗi tầng 1 kéo Agent sai theo (thiên lệch); chi phí gấp đôi.
- **Prompt cho Agent dựng bằng template trong Go**, không để LLM viết, để tất định và tái hiện được. Agent tự xuất kết quả định vị trước khi sửa, trong cùng một lần chạy.
- Câu hỏi nghiên cứu: *Khi Agent đã thấy toàn bộ code, gom nhóm + xếp hạng crash còn giúp được gì?*

## Quyết định đã chốt (cập nhật 2026-11-24)
1. **Agent nhận top 3 file của `crashrank.Rank`**, truyền thẳng, không qua LLM trung gian. "Top 3" = 3 file đầu theo điểm file, mỗi file kèm tối đa 3 hàm (dòng, điểm). Khớp với 3 file bài C đưa vào prompt và với metric file@3. Chặn trên của gợi ý này là file@3 của ranking: 100% ở lớp 1, 78% ở lớp 2.
2. **Bước 8 tạm hoãn**: giữ nguyên cách sắp hàm hiện tại (trọng số `share/(1+dist)`), vốn lệch khỏi bài C. Số B1 phải ghi chú điều này.
3. **Bỏ E2** (gợi ý của LLM1 cho Agent). Còn B0, B1, E0, E1. LLM1 chỉ còn trong B1, để lấy số tái hiện bài C.
4. **`plan_crash/crash_repro` là bản chính.** `gofl/` ít hơn và không có file nào mà `crash_repro` thiếu. Xoá `gofl/` là việc cuối cùng, sau khi commit, test pass và có xác nhận (Bước 13).
5. Mọi thay đổi ghi trong `crash_repro/docs/DECISIONS.md` (#8–#11).

## Trạng thái (2026-11-24)
- Giai đoạn 1 (bước 1–7) xong. `results/` chỉ có kết quả không LLM ngày 16/11; chưa có số cho B1, E0, E1.
- **Bước 10 đã xong (2026-11-24):** `crash_repro/internal/agentprompt` (`Hints`, `Build`, chế độ E0/E1), golden test C9 ở `testdata/C9_E0.golden`, `C9_E1.golden`. Cờ `gofl crash -agent-prompt` in prompt E1 của nhóm lớn nhất. `goflbench -agent-prompt-dir results/prompts` ghi 44 prompt (22 ca × E0/E1), đã kiểm không có `BUG:`. `bench.AgentWorkspace` tạo bản sao dự án không có `bugs.json` và `// BUG:` (giữ số dòng), có test. Số Acc@k không LLM không đổi.
- **Bước 11 đã xong (2026-11-24):** `internal/agentrun` + `cmd/goflagent`. Agent là Claude Code headless (`claude -p`), chạy nội bộ, model cố định `claude-sonnet-5-5`, trong bản sao dự án ở thư mục tạm (không `bugs.json`, không `// BUG:`), quyền chỉ gồm đọc/sửa trong workspace và `go`, 3 lần mỗi ca và chế độ, trần 2 USD mỗi lần. Chấm: định vị (`gofl-localize.json`), và "sửa đúng" = crash hết + test baseline vẫn pass + không sửa file test + sửa đúng hàm trong đáp án.
- **Kết quả E0 và E1 (B0 và B1 chưa chạy), lớp 2, 9 ca × 3 lần = 54 lần chạy, Sonnet** (`results/agent_realworld/`, tổng hợp bằng `goflagent -summary`):

  | | Sửa đúng (≥2/3) | func@1 | func@3 | file@3 | USD/lần | Số bước | Giây | Token vào | Token ra |
  |---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
  | E0 (stack thô) | 9/9 | 9/9 | 9/9 | 9/9 | 0,097 | 8,2 | 34 | 208.704 | 1.607 |
  | E1 (+ top 3 file) | 9/9 | 9/9 | 9/9 | 9/9 | 0,096 | 8,0 | 31 | 197.003 | 1.540 |

  Từng lần chạy: 53/54 lần có func@1 đúng, chỉ W9 E0 lần 1 sai (E1 không có lần nào sai); mọi lần đều sửa đúng. **Kết luận trung thực: với Agent thấy toàn bộ code, top 3 không làm tăng độ chính xác (đã chạm trần) và chỉ giảm chi phí khoảng 1% (0,097 → 0,096 USD, 8,2 → 8,0 bước, 34 → 31 giây), nằm trong nhiễu.** Prompt E1 chỉ dài hơn khoảng 700 byte. Token vào khoảng 200 nghìn mỗi lần chủ yếu là cache của Claude Code, không do prompt quyết định.
- **Hạn chế:** một model, 9 ca nên một ca bằng 11 điểm; chưa đọc tay ngữ nghĩa từng bản vá (điều kiện (c) chỉ kiểm tra đúng hàm); dự án lớp 2 dựa trên RealWorld công khai nên model có thể đã quen; comment có sẵn trong mã (ví dụ ở `GetUserByID`) nêu ý đồ sai. Lần thử đầu dùng Opus (`results/agent_pilot/`, lớp 1) không gộp vào số trên. Tiến trình chính từng bị hệ thống dừng vì thiếu bộ nhớ, đã chạy tiếp bằng `-resume`.
- **Extension VS Code đã viết lại (2026-11-24, `extension/` (ngang hàng với `plan_crash/`, có README hướng dẫn), v0.1.0, `.vsix` 24 KB):** (1) lấy crash log (vùng chọn, clipboard, file log, hoặc chạy `go test`) → `gofl crash -agent-prompt -format json` → hiện và copy prompt cho Agent; (2) ghi chú trực quan trên file nghi ngờ: cam = hàm gofl chỉ ra (top 3 file đã đưa cho Agent), xanh dương = chỗ Agent nói là lỗi (`gofl-localize.json`), xanh lá/đỏ = dòng Agent thêm/xoá so với bản lúc phân tích, kèm lệnh mở diff. Cờ CLI mới: `-agent-mode e0|e1`, `-repro`, và `-agent-prompt -format json` (xem `docs/CONTRACT.md`). Thiết kế tách phần thuần (có test `node --test`, 30 test) khỏi phần VS Code; ghi chú vẽ từ một `Session` không biết gì về crash nên gắn được `gofl perf` của nhóm E (hướng dẫn trong README của extension). Chưa chạy thử trong một VS Code đang mở.
- Bước 8 hoãn. Bước 9 (B1) chưa làm: dùng Claude, cần một `llm.Provider` gọi `claude -p` (không đặt được nhiệt độ 1,0, ghi model ID). Bước 12 chỉ còn thiếu B1 và đọc tay bản vá.
- Bước 13 (xoá `gofl/`) chưa làm: cần commit và xác nhận trước.

## Đã làm trong buổi 2026-11-19 (`crashrank`, đồng bộ `gofl` và `plan_crash`)
- Điểm theo hàm: `w = share/(1+dist)`, chuẩn hóa để hàm tốt nhất mỗi file có w = 1; `Score(fn) = Score(file) × w`. Thứ tự file không đổi.
- Không cắt giữa các hàm hòa điểm ở ngưỡng `MaxFuncsPerFile` (trước đây C9 mất `Refresh` chỉ vì tên).
- Không xếp hạng frame `_test.go`, trừ khi stack chỉ có file test.
- Sửa comment C9 trong `hard.go` (gộp ở level 4, không phải 3; giữ nguyên số dòng để `bugs.json` khớp).
- Test: `TestRankC9FunctionScoresAndTies`, `TestRankOnlyTestFiles`. `goflbench` không LLM cho Acc@k không đổi so với `results/*_nollm.txt`.

## Bước 8. Chốt độ trung thành của baseline (**HOÃN** theo quyết định 2026-11-24; giữ nguyên code hiện tại)
- Bài C: hàm được gợi ý "according to their frequency". Bản sửa ngày 2026-11-19 sắp hàm theo trọng số có tính khoảng cách, tức **lệch khỏi bài**.
- Sửa: sắp hàm theo tần suất → khoảng cách → tên (như bài); trọng số `w` chỉ dùng để tính cột Score.
- Ghi vào `docs/DECISIONS.md` các điều chỉnh có chủ đích so với bài: bỏ `_test.go`, giữ hàm hòa điểm, LLM trả vị trí JSON thay vì patch (`llm/prompt.go`).

## Bước 9. Chạy B1: tái hiện bài C có LLM (API ngoài)
- `goflbench -llm api`, chạy đủ 4 cấu hình (`llm.AllConfigs()`: ST, ST+RoSFM, SM, SM+RoSFM), mỗi cấu hình 5 lần, nhiệt độ 1,0.
- Lưu vào `results/`: model ID cụ thể, ngày chạy, nhiệt độ, **câu trả lời thô cả 5 lần**, token và chi phí.
- Trước lần gửi thật đầu tiên: chạy dry run, duyệt chính xác dữ liệu sẽ rời khỏi máy (mã 3 file, đường dẫn, nội dung panic). `Redact` không che mã nguồn.
- Kiểm tra điều khoản của nhà cung cấp API (lưu trữ dữ liệu, không dùng để huấn luyện). Key chỉ để trong `GOFL_LLM_*`, không commit.
- Ghi chú rủi ro: `goflrealworld`, nếu lấy từ repo công khai, có thể đã nằm trong dữ liệu huấn luyện của model.
- Lỗi API thì giữ nguyên kết quả ranking (hành vi hiện tại, giữ nguyên).

## Bước 10. Template prompt cho Agent (`internal/agentprompt`, **xong 2026-11-24**)
- Đầu vào: `crashrank.Group`, kết quả `Rank` của nhóm, lệnh tái hiện (`trigger` và `related_triggers` của `bench.Bug`), danh sách test phải tiếp tục pass.
- `TopFiles(ranked, 3, 3)`: gộp finding cấp hàm về 3 file đầu (finding đầu của mỗi file mang điểm file), mỗi file tối đa 3 hàm.
- `Build(in, mode)` với `Mode = E0 | E1`, **cùng một template**: E0 chỉ bỏ mục 3 dưới đây, để E1 − E0 đo đúng giá trị của top 3.
- Nội dung template:
  1. Crash: nhóm, exception, số event, **lệnh tái hiện của mọi entry point trong nhóm**.
  2. Stack: trace đại diện + phần khác nhau giữa các trace.
  3. (Chỉ E1) top 3 file kèm hàm và điểm, kèm cảnh báo: ranking chỉ phản ánh đường lan của crash; giá trị lỗi (nil, index, capacity…) có thể được tạo ở hàm đã return trước khi crash, nên phải lần ngược nguồn gốc giá trị; nguyên nhân gốc có thể nằm ngoài top 3.
  4. Yêu cầu theo thứ tự: tái hiện → định vị, ghi `gofl-localize.json` `{"locations":[{file,function,line}],"explanation"}` → sửa tại nguyên nhân gốc (không chỉ thêm nil-check/recover ở điểm crash) → chạy lại lệnh tái hiện và các test phải pass.
- Không đưa mã nguồn vào prompt (Agent có full scope, tự đọc). Chạy `llm.Redact` trên phần text lấy từ crash.
- Golden test cho C9 (3 entry point, nguyên nhân gốc `NewSession:57` nằm ngoài stack) ở cả E0 và E1; test `TopFiles` (nhiều hàm cùng file, hòa điểm, ít hơn 3 file, chỉ có file test). Không gọi API.
- Nối vào `gofl crash` (cờ in prompt E1 cho nhóm lớn nhất) và `goflbench` (ghi prompt E0/E1 từng ca ra `results/prompts/`). Chỉ sinh prompt, không gọi Agent.
- **Chống rò đáp án:** workspace của Agent là bản sao dự án đã xoá mọi chú thích `// BUG:` (giữ số dòng, tái dùng `crashflow.LLMOptions.Filter`) và **không có `bugs.json`**. Lỗi này đã từng làm lộ đáp án ở đường LLM; Agent đọc cả repo nên rủi ro còn lớn hơn. Có test kiểm tra workspace.

## Bước 11. Chạy Agent và kiểm chứng
- Chạy Agent trên từng ca; `internal/runner` kiểm chứng sau khi sửa.
- Một ca **sửa đúng** khi đủ 3 điều kiện: (a) crash không còn; (b) các test khác vẫn pass; (c) hàm bị sửa nằm trong `bugs.json`, tức sửa nguyên nhân gốc. Ví dụ: C9 thêm nil-check vào `Token` là vá triệu chứng, không tính.
- Khôi phục mã nguồn (git worktree/bản sao) giữa các ca.

## Bước 12. Thí nghiệm so sánh
| Ký hiệu | Flow | Vai trò |
|---|---|---|
| B0 | Frame user đầu tiên | Baseline ngây thơ (đã có) |
| B1 | Gom nhóm → ranking → LLM1 (API ngoài), 4 cấu hình, 5 lần, ≥3/5 | Tái hiện bài C |
| E0 | Agent chỉ nhận stack thô (không gom nhóm, không ranking) | Đo giá trị của crash mining |
| E1 | Gom nhóm → ranking → top 3 file → Agent (template bước 10) → kiểm chứng | Mở rộng chính |
| ~~E2~~ | ~~E1 + gợi ý LLM1~~ | **Đã bỏ** (2026-11-24) |
- Chỉ số: Acc@k định vị cấp hàm (ranking, LLM1 trong B1, `gofl-localize.json` của Agent); tỉ lệ sửa đúng (bước 11); token, thời gian, số bước của Agent; chi phí API.
- Chia kết quả theo hai nhóm: **crash ≠ fix** (C9, C10, C11) và **crash = fix**. Nhóm thứ nhất là nơi các phương pháp khác nhau.
- Diễn giải: E1 − E0 là giá trị của top 3 khi Agent có full scope. Nếu E1 không hơn E0 rõ ràng, kết luận gom nhóm + xếp hạng không giúp Agent sửa lỗi; chúng vẫn có giá trị cho triage hàng loạt. LLM1 chỉ phục vụ số liệu tái hiện bài C (B1).
- Với 9 ca ở lớp 2, một ca bằng 11 điểm: mọi so sánh chỉ là bằng chứng sơ bộ.

## Bước 13. Dọn `gofl/` (làm cuối cùng, cần xác nhận)
Chỉ xoá `D:\NL&TKTT\gofl` sau khi: `crash_repro` đã commit, `go vet ./... && go test ./...` pass, và các thư mục bàn giao (`C_TV3_llm_logprofile`, `C_week1_crashsig_TV2`, `Week1/*`) không còn phụ thuộc đường dẫn `gofl/`. Lịch sử git giữ bản cũ.

## Vai trò hợp lệ của một tầng LLM phụ (nếu cần sau này; E2 đã bỏ nên không dùng trong thí nghiệm hiện tại)
- **Trước Agent:** triage hàng loạt nhóm crash (rẻ), chọn nhóm nào đáng giao cho Agent. Không đưa đáp án của nó cho Agent.
- **Giữa hai giai đoạn:** khi cần người duyệt giữa bước định vị và bước sửa.
- **Sau Agent:** model khác đọc patch để phát hiện vá triệu chứng (ý kiến thứ hai độc lập).

## Câu hỏi còn mở
- Agent gọi model qua API bên ngoài hay chạy nội bộ? Nếu nội bộ, LLM1 (API ngoài) là điểm duy nhất mã nguồn rời khỏi máy và cần người phụ trách bảo mật duyệt. Nếu Agent cũng gọi API ngoài, LLM1 chỉ thêm rủi ro khi nó dùng nhà cung cấp khác.
- Hướng không dùng LLM cho nguyên nhân gốc ngoài stack (C9, C10): heuristic `go/ast` + `go/types`. Với nil-deref trên field, tìm nơi khởi tạo kiểu chứa field mà bỏ trống field đó. Chỉ làm nếu cần baseline mạnh hơn không phụ thuộc LLM.

## Verification giai đoạn 2
- `cd plan_crash/crash_repro && go vet ./... && go test ./...` sau mỗi bước, (không còn đồng bộ sang `gofl/`: `crash_repro` là bản chính).
- Bước 9 và 12 cho bảng số liệu; mọi lần chạy có LLM/Agent đều lưu đầu ra thô vào `results/`.
