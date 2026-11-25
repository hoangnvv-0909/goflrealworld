# Extension VS Code cho gofl

Extension làm hai việc, cho `gofl crash` (bài C) trước. Bài E (`gofl perf`) gắn vào sau qua cùng các thành phần.

1. **Từ crash log sinh ra prompt cho Agent sửa lỗi.** Bạn đưa một stack trace (vùng chọn, clipboard, file log, hoặc để gofl chạy `go test`). Extension gọi `gofl crash -agent-prompt` và hiện prompt, sẵn sàng dán vào Claude Code hoặc Agent khác.
2. **Ghi chú trực quan trên file nghi ngờ để so sánh phần Agent sửa.** Trên chính file trong editor:
   - **Cam:** hàm gofl chỉ ra, kèm hạng và điểm. Ở chế độ E1 đây là top 3 file đã đưa cho Agent.
   - **Xanh dương:** chỗ Agent nói là lỗi, đọc từ `gofl-localize.json` (prompt yêu cầu Agent ghi file này trước khi sửa).
   - **Xanh lá / đỏ:** dòng Agent thêm hoặc xoá so với bản file lúc gofl phân tích. Rê chuột vào để xem đoạn code cũ.

Nhờ đó bạn thấy trong một chỗ: gofl nhìn ở đâu, Agent nhìn ở đâu, và Agent thực sự sửa gì.

## 1. Chuẩn bị

Cần ba thứ: Go, Node.js và VS Code (từ 1.90).

### 1.1. Build binary `gofl`

Extension gọi binary `gofl`, và binary phải có các cờ `-agent-prompt`, `-agent-mode`, `-repro`. Các cờ này nằm trong `plan_crash/crash_repro`.

```
cd D:\NL&TKTT\plan_crash\crash_repro
go build -o gofl.exe ./cmd/gofl
```

Rồi chọn một trong hai cách:
- Thêm thư mục chứa `gofl.exe` vào `PATH`.
- Hoặc đặt đường dẫn đầy đủ vào cài đặt `gofl.path`, ví dụ `D:\NL&TKTT\plan_crash\crash_repro\gofl.exe`.

Kiểm tra nhanh: `gofl crash -h` phải liệt kê `-agent-prompt`. Nếu không thấy, bạn đang dùng binary cũ (ví dụ `gofl.exe` ở thư mục gốc `D:\NL&TKTT`, build trước khi có các cờ này).

### 1.2. Build và cài extension

```
cd D:\NL&TKTT\extension
npm install
npm test                 # 41 test, không cần mở VS Code
npm run package          # tạo gofl-vscode-0.2.0.vsix
code --install-extension gofl-vscode-0.2.0.vsix
```

Trong thư mục này đã có sẵn `gofl-vscode-0.2.0.vsix`, nên có thể cài ngay bằng lệnh cuối. Cài xong, khởi động lại VS Code.

Muốn thử từ mã nguồn mà không cài: mở thư mục `extension` trong VS Code, bấm **F5**, cửa sổ mới (Extension Development Host) sẽ có extension.

> **Lỗi hay gặp trên Windows:** đường dẫn có ký tự `&` (như `NL&TKTT`) làm hỏng các lệnh `npm run ...` và `npx ...` (báo `'...\.bin\' is not recognized`). Khi đó chạy công cụ trực tiếp bằng node:
> ```
> node node_modules/typescript/bin/tsc -p .
> node --test "out/test/*.test.js"
> node node_modules/@vscode/vsce/vsce package --allow-missing-repository --skip-license
> ```

## 2. Cách dùng

### 2.1. Sinh prompt từ crash log

1. Mở **thư mục dự án Go** trong VS Code (thư mục có `go.mod`, nơi crash xảy ra). Đường dẫn trong kết quả tính theo thư mục này.
2. Có hai cách bắt đầu:
   - Chọn đoạn stack trace trong bất kỳ editor nào, nhấp phải, chọn **gofl: Generate Agent prompt from selection**.
   - Hoặc mở Command Palette (`Ctrl+Shift+P`) và chạy **gofl: Generate Agent prompt from a crash log**. Cũng có thể bấm nút lấp lánh ở thanh tiêu đề của view **gofl** trong Explorer.
3. Nếu dùng cách thứ hai, chọn nguồn log:

   | Nguồn | Khi nào dùng |
   |---|---|
   | Selected text | Đã chọn sẵn một đoạn |
   | Clipboard | Bạn vừa copy stack trace từ terminal |
   | A log file | File log, hoặc đầu ra của `go test -json` |
   | The open file | File đang mở chính là log |
   | Run go test now | gofl tự chạy test và đọc crash. Nhập gói (ví dụ `./...`) và regexp tên test nếu muốn |

4. Nhập **lệnh tái hiện crash** (có thể bỏ trống), ví dụ `go test ./crash -run TestNth`. Log không chứa lệnh này, còn Agent dùng nó để kiểm tra bản sửa. Muốn khỏi bị hỏi, đặt `gofl.repro`. Nếu chọn "Run go test now" thì không cần nhập, gofl tự điền.
5. Prompt mở ở cột bên cạnh. Bấm **Copy prompt** trong thông báo (hoặc lệnh **gofl: Copy Agent prompt**), rồi dán vào Agent.

### 2.2. Cho Agent làm việc

Chạy Agent (ví dụ Claude Code) **trong chính thư mục dự án** đang mở, và dán prompt. Prompt yêu cầu Agent:

1. Tái hiện crash bằng lệnh đã cho.
2. Ghi `gofl-localize.json` ở thư mục gốc dự án **trước khi sửa code**:
   ```json
   {"locations": [{"file": "crash/hard.go", "function": "NewSession", "line": 57}], "explanation": "..."}
   ```
3. Sửa nguyên nhân gốc, không chỉ thêm kiểm tra nil ở điểm crash.
4. Chạy lại lệnh tái hiện và các test.

### 2.3. Xem và so sánh

Trong lúc Agent làm, extension tự cập nhật (không cần bấm gì):

- Trên file nghi ngờ hiện các màu như mục đầu tài liệu. Nhãn nằm cuối dòng đầu của vùng, ví dụ `gofl suspect #1 · Session.Token · score 0.69`.
- View **gofl** trong Explorer có các mục: file gofl nghi ngờ, **Agent's localisation**, **Agent's changes**, **Agent prompt**, và bảng xếp hạng đầy đủ của gofl. Bấm vào một mục để nhảy tới dòng.
- **gofl: Compare the Agent's changes** (nút so sánh ở thanh tiêu đề view) mở diff thông thường của VS Code: bên trái là file lúc phân tích, bên phải là file hiện tại. Nếu nhiều file đổi thì hiện danh sách để chọn.
- **gofl: Reload the Agent's localisation** đọc lại `gofl-localize.json` bằng tay (bình thường tự đọc khi file xuất hiện hoặc đổi).
- **gofl: Clear notes** xoá toàn bộ ghi chú. Phiên làm việc được lưu theo workspace, nên đóng mở lại VS Code vẫn còn.

### 2.3. Chế độ prompt: E1 và E0

Cài đặt `gofl.agentMode`, hoặc lệnh **gofl: Choose prompt mode (E0 or E1)**:

| Chế độ | Prompt chứa | Dùng để |
|---|---|---|
| `e1` (mặc định) | Thông tin crash, stack, lệnh tái hiện, **và top 3 file gofl nghi ngờ** | Dùng gofl như thường |
| `e0` | Chỉ thông tin crash, stack, lệnh tái hiện | Đối chứng trong thí nghiệm ở `plan_crash/KE_HOACH_BAI_C.md` |

Ở E0, các file vẫn được tô màu cam, nhưng chú thích hiện "no (E0, crash facts only)" để bạn nhớ Agent không được gợi ý.

## 3. Cài đặt

| Cài đặt | Mặc định | Ý nghĩa |
|---|---|---|
| `gofl.path` | `gofl` | Đường dẫn binary gofl |
| `gofl.agentMode` | `e1` | `e1` hoặc `e0`, xem trên |
| `gofl.repro` | `[]` | Lệnh tái hiện crash. Có giá trị thì extension không hỏi nữa |
| `gofl.crashArgs` | `[]` | Cờ thêm cho `gofl crash`, ví dụ `["-profile", "svc.json"]`. `-dir` và `-format` được thêm tự động |
| `gofl.perfPath` | rỗng | Binary `gofl` của nhóm E (`plan_efficency/gofl-run`). Rỗng thì dùng `gofl.path`. `gofl crash` luôn dùng `gofl.path` |
| `gofl.perfArgs` | `[]` | Cờ thêm cho `gofl perf`, ví dụ `["-pkg", "./slow", "-setup", "docker compose up -d"]`. `perf`, `-dir`, `-out`, `-bench`, `-sizes` được thêm tự động |
| `gofl.perfBench` | rỗng | Tên benchmark mặc định |
| `gofl.perfSizes` | `100,10000` | Hai kích thước (nhỏ, lớn) mặc định |
| `gofl.notes.enabled` | `true` | Tắt thì không vẽ ghi chú trong editor |

Màu đổi được trong `workbench.colorCustomizations`: `gofl.suspectBackground`, `gofl.agentBackground`, `gofl.addedBackground`, `gofl.removedBackground` và bốn màu `...Foreground` tương ứng.

## 4. Xử lý sự cố

| Triệu chứng | Nguyên nhân và cách xử lý |
|---|---|
| `"gofl" not found` | Chưa có `gofl` trong `PATH`. Đặt `gofl.path` (mục 1.1) |
| `flag provided but not defined: -agent-prompt` (hoặc `-agent-mode`, `-repro`) | Binary cũ. Build lại từ `plan_crash/crash_repro`. Hai file `gofl.exe` đang có sẵn (trong `plan_crash/crash_repro` và ở `D:\NL&TKTT`) đều là bản cũ, chưa có các cờ này |
| `no crash found, so there is no Agent prompt` | Đoạn đưa vào không có `panic:` hoặc `fatal error:`. Chọn đủ cả dòng `panic:` và stack bên dưới |
| `not found in the open folder: ...` | VS Code đang mở sai thư mục. Mở đúng thư mục gốc dự án, rồi tạo lại prompt |
| Không thấy ghi chú màu | Mở đúng file nghi ngờ. Kiểm tra `gofl.notes.enabled`. Xem log ở **Output → gofl** |
| Agent đã ghi `gofl-localize.json` mà không thấy màu xanh dương | File phải nằm ở thư mục gốc dự án. Chạy **gofl: Reload the Agent's localisation** |
| Cảnh báo "Agent has not changed the suspect files yet" | Đúng như tên: Agent chưa sửa file nào trong số file gofl nghi ngờ. Nếu Agent sửa file khác, file đó không được so sánh |

Mọi lệnh extension gọi gofl, cùng stderr của gofl, đều ghi ở **Output → gofl**.

## 5. Cấu trúc mã

Nguyên tắc: phần nào quyết định được mà không cần VS Code thì là module thuần, có test `node --test`. File phụ thuộc VS Code chỉ vẽ và gọi lệnh.

| File | Vai trò | Cần VS Code |
|---|---|---|
| `src/model.ts` | Bản sao `internal/finding` và JSON của `gofl crash -agent-prompt -format json` | không |
| `src/parse.ts` | Kiểm tra hai loại JSON, từ chối `version` mới hơn | không |
| `src/args.ts` | Dòng lệnh gọi gofl | không |
| `src/runner.ts` | Chạy binary, đưa crash log qua stdin | không |
| `src/notes.ts` | **Quyết định vẽ gì trên một file**: suspect, agent, added, removed | không |
| `src/linediff.ts` | Diff theo dòng so với bản chụp | không |
| `src/localize.ts` | Đọc `gofl-localize.json` của Agent | không |
| `src/hints.ts` | Report thành "các file cần xem" (cho lệnh chưa có prompt) | không |
| `src/paths.ts` | `Location.file` và đường dẫn tuyệt đối | không |
| `src/decorations.ts` | Vẽ ghi chú | có |
| `src/tree.ts` | Cây ở side-bar | có |
| `src/extension.ts` | Lệnh, nối các phần, lưu phiên | có |

Trạng thái là một `Session`: các file gofl chỉ ra, bản chụp từng file, vị trí Agent nêu, prompt. Session lưu trong workspace state.

Hợp đồng với phía Go: `docs/CONTRACT.md` ở `plan_crash/crash_repro` (mục cờ `-agent-prompt`, `-agent-mode`, `-repro`). Khi `finding.SchemaVersion` đổi thì cập nhật `src/model.ts` và `SCHEMA_VERSION`.

## 6. Dùng `gofl perf` (nhóm E)

Binary E khác binary C. Build từ `plan_efficency/gofl-run/src`: `go build -trimpath -o gofl-perf.exe ./cmd/gofl`, rồi đặt `gofl.perfPath` trỏ tới nó. `gofl crash` vẫn dùng `gofl.path`.

1. Mở **thư mục gốc project Go** (có `go.mod`).
2. Chạy **gofl: Profile benchmark (gofl perf)** và chọn:
   - **Run a benchmark**: nhập tên benchmark và hai kích thước. Benchmark cần một sub-benchmark cho mỗi kích thước, ví dụ `b.Run("n=100", ...)`. Benchmark trùng tên ở nhiều gói thì thêm `["-pkg", "./slow"]` vào `gofl.perfArgs`.
   - **Two CPU profiles**: chọn file pprof cỡ nhỏ và cỡ lớn (không cần Go).
3. `gofl perf` ghi `hotspots.json`, `critical_paths.json` vào một thư mục tạm. `src/perf.ts` đổi chúng thành `Report` (`command: "perf"`) rồi dùng lại `Session`, ghi chú, cây và diff của bài C. Thư mục tạm bị xoá sau khi đọc.
4. Hàm chậm được tô cam, nhãn ghi *hotspot*, kèm hạng và điểm (độ tăng thời gian, chuẩn hoá 0..1). Ở chế độ benchmark, điểm và số liệu tính theo thời gian **mỗi thao tác** (`delta_per_op_seconds`), đúng như `gofl perf` xếp hạng.
5. Đường dẫn trong file pprof là của máy tạo ra nó. Nếu không nằm trong thư mục đang mở, extension thử khớp phần đuôi (ít nhất hai đoạn) với file trong thư mục; không khớp được thì báo cảnh báo.
6. Lỗi của `gofl perf` (`NO_BENCH`, `NO_MODULE`, `BUILD_FAIL`, ...) hiện kèm lý do cụ thể ở thông báo và **Output → gofl**.

Chưa có prompt cho Agent ở bài E. Phần đó dự kiến làm riêng (`src/perfprompt.ts`), dùng chung `Session`.

## 7. Giới hạn

- Vùng nghi ngờ được tìm bằng quy tắc văn bản (từ dòng `func` đến dấu `}` ở cột 0). Đúng với code Go đã `gofmt`, không cần language server.
- Prompt lấy từ **nhóm crash lớn nhất** của log, còn bảng xếp hạng ở cây gộp mọi nhóm. Log chứa nhiều crash không liên quan thì các file màu cam chỉ thuộc nhóm lớn nhất.
- Bản chụp lấy lúc sinh prompt. Nếu bạn tự sửa file nghi ngờ sau đó, chỗ bạn sửa cũng hiện như thay đổi.
- Đã kiểm tra bằng `node --test` và chạy thật binary `gofl` đầu cuối. Phần vẽ, cây và lệnh trong VS Code **chưa được chạy thử trong một VS Code đang mở**. Nếu gặp lỗi khi dùng, gửi nội dung **Output → gofl** (hoặc **Help → Toggle Developer Tools → Console**).
