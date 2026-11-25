# gofl perf (Nhóm E): báo cáo và hướng dẫn sử dụng

Bản đã ghép phần của Hưng, Long, Đức Anh và Hiếu. Các lệnh ở các mục sau chạy từ gốc mã nguồn (mục 2). Số liệu đo trên Linux amd64, Go 1.26.3, ngày 23 và 24/11.

## Tóm tắt

- **Công cụ làm gì:** so sánh một workload Go ở hai kích thước (nhỏ, lớn), cho biết hàm nào tăng thời gian nhiều nhất và đường gọi tới hàm đó.
- **Hai cách dùng:** (1) có sẵn hai CPU profile (pprof), không cần mã; (2) có benchmark, công cụ tự chạy ở hai kích thước.
- **Đầu ra:** `report.md` và `report.json` (có tên hàm, có thể băm tên), `diagnostics.json` (không chứa tên, luôn an toàn để gửi).
- **Trạng thái:** đã ghép đủ bốn phần, 12 gói có test đều đạt, đã chạy tay 12 kịch bản (mục 7).
- **Ba điều khác kế hoạch ban đầu** (mục 8): xếp hạng mặc định theo giây mỗi thao tác; hai bộ đọc pprof gộp thành một; điểm đường gọi đổi đơn vị theo cách xếp hạng. Ngoài ra khi chạy thử phát hiện và sửa một lỗi về thư mục dự án (mục 8, hàng cuối).
- **Còn lại:** thống nhất với Hưng đối tượng `bench` trong `run.json`; chưa thử trên Windows và macOS.

## 1. Công cụ làm gì

```
profile ──► compute ──► paths ──► report ──► diagnostics
 Hưng        Long       Đức Anh    Hiếu        Hưng
```

| Bước | Việc | Ghi vào `out/` |
|---|---|---|
| profile | chạy benchmark ở hai kích thước, hoặc kiểm tra hai pprof cho sẵn | `base.pprof`, `scaled.pprof`, `run.json`, `*.log` |
| compute | tính Δself và NDC, chọn top-5 hàm | `hotspots.json` |
| paths | tìm đường gọi từ điểm vào tới từng hotspot | `critical_paths.json` |
| report | gộp thành báo cáo đọc được | `report/report.md`, `report/report.json` |
| diagnostics | đếm số liệu, không chứa tên | `diagnostics.json` |

## 2. Cài và chạy nhanh

Trong tài liệu này, "gốc mã nguồn" là thư mục `gofl/` của repo, và là thư mục `src/` trong `gofl-run.zip`. Các đường dẫn như `testdata/...`, `scripts/build.go` tính từ đó.

**Dùng tệp có sẵn trong zip** (không cần Go khi chạy chế độ 2 pprof). Từ thư mục giải nén, chọn tệp trong `bin/` đúng máy (`gofl-linux-amd64`, `gofl-darwin-arm64`, `gofl-windows-amd64.exe`, …):

```sh
./bin/gofl-linux-amd64 perf -base src/testdata/profiles/dedup_base.pprof -scaled src/testdata/profiles/dedup_scaled.pprof
```

**Tự build** (cần Go 1.26 trở lên), từ gốc mã nguồn (`gofl/` trong repo, `src/` trong zip):

```sh
go build -o gofl ./cmd/gofl            # Windows: -o gofl.exe
./gofl perf -base testdata/profiles/dedup_base.pprof -scaled testdata/profiles/dedup_scaled.pprof
```

Kết quả đúng: `status: ok`, mã thoát 0, thư mục `out/` có `report/report.md`. Cảnh báo "dưới 1,5 lần" ở ví dụ này là bình thường.

Build mọi hệ điều hành vào `bin/`: `go run scripts/build.go` (một đích: `-only linux/amd64`). Kiểm tra mã: `go vet ./... && go test ./...`, khoảng 1 phút.

## 3. Hướng dẫn sử dụng

### 3.1 Đã có hai pprof

```sh
gofl perf -base a.pprof -scaled b.pprof
```

Hai profile là cùng workload ở hai kích thước; `b.pprof` phải đo lâu hơn hoặc nặng hơn. Nếu độ dài hai profile chênh dưới 1,5 lần, công cụ cảnh báo vì Δ khi đó chỉ là dịch chuyển tỉ lệ. Điểm vào của đường gọi là gốc cây gọi, thu hẹp bằng `-entry`.

### 3.2 Có benchmark

```sh
gofl perf -pkg ./internal/report -bench BenchmarkBuildRows -sizes 100,10000
```

- Chạy tại thư mục dự án (nơi có `go.mod`) hoặc đặt `-dir`. `-pkg` bỏ trống thì tìm trong `./...`.
- Benchmark có một sub-benchmark cho mỗi kích thước (`b.Run("size=100", …)`), hoặc đọc kích thước từ biến môi trường `GOFL_SIZE`.
- Điểm vào của đường gọi tự lấy là hàm `-bench`.
- Chưa có benchmark: chép `docs/bench_template_test.go.txt` cạnh mã cần đo, đổi đuôi `.go`, đổi tên gói, điền `TODO`, bỏ `b.Skip`.
- Mẫu để thử: `testdata/benchmarks/{linearlookup,duplicates,sortedinsert}` (mỗi cái có một hàm chậm đã biết) và `testdata/sample`.

### 3.3 Cấu hình và cờ

Chép `gofl.example.yaml` thành `gofl.yaml`; cờ trên dòng lệnh ghi đè tệp này.

| Cờ | Ý nghĩa | Mặc định |
|---|---|---|
| `-base`, `-scaled` | chế độ 2 pprof | |
| `-bench`, `-sizes a,b` | chế độ benchmark | |
| `-dir`, `-pkg`, `-out` | thư mục dự án, gói, thư mục kết quả | `.`, tìm hết, `out` |
| `-benchtime`, `-count`, `-cpu`, `-timeout`, `-setup`, `-env` | chuyển cho `go test` và bước chuẩn bị | 1s, 1, GOMAXPROCS, 10m |
| `-topk N` | số hotspot | 5 |
| `-rank-by perop\|raw` | cách xếp hạng (mục 5.1) | benchmark: `perop`; 2 pprof: `raw` |
| `-attribute-runtime` | tính thời gian `runtime.*` cho hàm gọi nó | tắt |
| `-entry F` | hàm xuất phát của đường gọi | hàm `-bench`; pprof: gốc cây |
| `-paths-method paper\|stack` | cách tìm đường (mục 5.3) | `paper`, rơi về `stack` nếu cần |
| `-hash-names` | băm tên hàm và tệp trong `report/` | tắt |

Lệnh riêng: `gofl preflight` (kiểm tra máy), `gofl profile`, `gofl compute`, `gofl diagnostics`, `gofl version`.
Mã thoát: 0 thành công; 1 chạy lỗi (mã lỗi trong `run.json` và `diagnostics.json`); 2 sai cờ hoặc `gofl.yaml`.

## 4. Đọc kết quả

Ví dụ thật: `gofl perf -pkg ./testdata/benchmarks/linearlookup -bench BenchmarkLinearLookup -sizes 100,10000`.

| Hạng | Hàm | Mỗi thao tác, tải nhỏ (s) | Mỗi thao tác, tải lớn (s) | Δ mỗi thao tác (s) | NDC |
|---:|---|---:|---:|---:|---:|
| 1 | `linearlookup.CountMatches` | 1,694e-06 | 0,012251 | 0,0122493 | 100% |

Đường gọi: `BenchmarkLinearLookup.func1 -> CountMatches`, điểm 0,0116 (giây mỗi thao tác).

- **Δself** = giây self của hàm ở tải lớn trừ giây ở tải nhỏ. Self là thời gian chạy ngay trong hàm, không tính hàm nó gọi.
- **NDC** = Δ của hàm chia tổng Δ dương của mọi hàm. Gần 100% nghĩa là gần như toàn bộ phần tăng đến từ hàm đó.
- **Đường gọi**: chuỗi `tệp(hàm) -> …` từ điểm vào xuống hàm đích. **Điểm** là tổng Δ của các khung trên đường (`paper`) hoặc Δ của chính stack đó (`stack`), cùng đơn vị với cách xếp hạng hotspot.
- Khi xếp theo thao tác, bảng hotspot dùng các cột "mỗi thao tác" như trên; khi xếp theo giây thô, dùng cột self-time và Δself.
- `report.json` cùng nội dung ở dạng máy đọc, thêm `schema_version` và `names_hashed`. Tên bị băm (`-hash-names`) có dạng `fn_…`, `file_…`; khóa băm ngẫu nhiên mỗi lần chạy nên hai lần chạy không so khớp tên được.

## 5. Ba quyết định thiết kế

### 5.1 Xếp hạng theo thao tác (`perop`) hay giây thô (`raw`)

| | Công thức | Dùng khi |
|---|---|---|
| `raw` | Δ = giây self ở profile lớn − giây self ở profile nhỏ (hợp đồng v0 và paper) | chế độ 2 pprof; đối chiếu với `go tool pprof -base` |
| `perop` | đổi mỗi profile sang giây mỗi thao tác của benchmark rồi trừ. Hệ số = `ns_per_op ÷ T`, `T` là thời gian các mẫu nằm dưới hàm `Benchmark…` (`diffprof.Scale`) | chế độ benchmark (mặc định) |

**Vì sao `perop` là mặc định ở chế độ benchmark.** Go chọn số lần lặp `b.N` để mỗi kích thước chạy khoảng 1 giây. Ở kích thước 100 chạy 688.000 lần (1,7 µs mỗi lần), ở 10.000 chạy 100 lần (11,7 ms mỗi lần): mỗi lần chậm hơn hơn 6.000 lần nhưng tổng giây gần bằng nhau. Δ thô vì thế chủ yếu là nhiễu, cùng một lệnh lúc ra hotspot lúc `NO_POSITIVE_DELTA`.

| Benchmark | `raw` | `perop` (3 lần chạy) |
|---|---|---|
| LinearLookup | `CountMatches` hoặc `NO_POSITIVE_DELTA`, tùy lần | `CountMatches`, 3/3 |
| SortedInsert | `SortByInsertion` | `SortByInsertion`, 3/3 |
| Duplicates | `NO_POSITIVE_DELTA` | `Unique`, 3/3 |
| sample/slow | `NO_POSITIVE_DELTA` | `contains` (94%), `Dedup`, `BenchmarkDedup.func1` |

- Chế độ 2 pprof không có `ns_per_op` nên mặc định `raw`; đặt `-rank-by perop` ở đó là lỗi dùng sai (thoát 2).
- Muốn dùng `raw` thì đặt `-rank-by raw`. Khi `raw` không ra gì mà `perop` ra, công cụ in gợi ý.
- Nếu `run.json` thiếu `bench.base_ns_per_op` hoặc `scaled_ns_per_op`, công cụ tự quay về `raw`.

**Hạn chế của `perop`: giữ phần chuẩn bị trong benchmark nhẹ.** Một benchmark thường có hai phần:

```go
func BenchmarkSearch(b *testing.B) {
    data := buildIndex(1_000_000)   // (1) chuẩn bị: dựng dữ liệu, chạy đúng 1 lần
    b.ResetTimer()                  // (2) "xóa đồng hồ về 0, tính giờ từ đây"
    for i := 0; i < b.N; i++ {      // (3) phần được đo: lặp b.N lần
        Search(data, key)
    }
}
```

Go chỉ tính giờ phần (3): "10 ms mỗi thao tác" là thời gian phần (3) chia cho `b.N`. Phần (1) vẫn chạy nhưng không vào con số đó, dù nó có thể nặng, ví dụ dựng một triệu phần tử mất cả giây. Công cụ lại không tính giờ mà đọc CPU profile, tức bản ghi chương trình đang ở hàm nào, lấy mẫu liên tục suốt lúc benchmark chạy. Profile không biết dòng `b.ResetTimer()`, nên mẫu của phần (1) và (3) trộn vào nhau.

Hệ quả, với 10 ms mỗi thao tác và `buildIndex` chiếm 20% số mẫu:
- `Search` chỉ chiếm 80% nên bị báo 8 ms thay vì 10 ms.
- `buildIndex` chỉ chạy một lần, không lặp theo `b.N`, nhưng bị quy thành "2 ms mỗi thao tác". Nó có thể lọt vào bảng hotspot, và báo cáo sẽ chỉ bạn tối ưu một hàm chỉ là phần chuẩn bị của bài đo, không phải mã chạy khi phục vụ yêu cầu thật.

Ví dụ trên là giả định để minh họa; chưa đo trên benchmark thật có phần chuẩn bị nặng. Ba benchmark mẫu của dự án chỉ tạo một hoặc hai mảng số nguyên trước `b.ResetTimer()`, nên gần như không có mẫu nào ở phần (1) và sai lệch không đáng kể. Các vòng chạy thử mà Go dùng để chọn `b.N` không gây lệch, vì chúng chạy đúng đoạn mã ở phần (3) nên giữ nguyên tỉ lệ giữa các hàm. Cách giảm lệch: giữ phần (1) ngắn nhất có thể.

### 5.2 Bộ lọc nhiễu

Mặc định `runtime.*`, `internal/runtime/*` và `testing.*` bị bỏ khỏi Δ và NDC (paper chỉ bỏ khung VizTracer và script chạy). Hệ quả: chi phí cấp phát và GC không vào Δ. `-attribute-runtime` tính thời gian `runtime.*` cho hàm gọi gần nhất, chỉ ảnh hưởng `hotspots.json`.

### 5.3 Cách tìm đường gọi

- `paper` (theo paper): đi từ điểm vào xuống từng lần xuất hiện của hotspot trong cây gọi. Nếu điểm vào không bao trùm hết (ví dụ hàm nóng chạy trong goroutine riêng) thì lỗi `ENTRY_NOT_COVERING`.
- `stack` (dự phòng): lấy các stack có hotspot ở lá, cắt từ điểm vào, gộp theo chữ ký.
- Không đặt `-paths-method`: thử `paper`, gặp `ENTRY_NOT_COVERING` thì dùng `stack` kèm cảnh báo. `run.json` ghi phương pháp thực sự đã dùng. Muốn bắt buộc `paper`: `-paths-method paper`.

## 6. Mã lỗi

| Mã | Nghĩa | Việc nên làm |
|---|---|---|
| `NO_GO`, `NO_MODULE`, `NO_BENCH` | thiếu Go, thiếu `go.mod`, không thấy benchmark | kiểm `-dir`, `-pkg`, `-bench`; chạy `gofl preflight` |
| `BUILD_FAIL`, `TEST_NEEDS_ENV`, `TIMEOUT` | biên dịch lỗi, test cần biến môi trường hay dịch vụ, quá thời gian | xem `out/build.log`, `bench_*.log`; thêm `-setup`, `-env`, tăng `-timeout` |
| `NO_PROFILE` | pprof hỏng hoặc không có mẫu | tăng `-benchtime`, kiểm tệp |
| `NO_POSITIVE_DELTA` | không hàm nào tăng | thử `-rank-by perop`, kích thước cách xa hơn, hoặc `-attribute-runtime` |
| `ENTRY_NOT_COVERING` | điểm vào không bao trùm hotspot (chỉ khi `-paths-method paper`) | bỏ cờ để rơi về `stack`, đổi `-entry` |

## 7. Kiểm thử

**Tự động:** `gofmt -l .` rỗng; `go vet ./...` sạch; `go test -count=1 ./...` đạt 12 gói có test; `go vet` đạt trên linux, darwin, windows với amd64 và arm64.

**Chạy tay** bằng tệp nhị phân build từ mã này:

| # | Kịch bản | Kết quả |
|---|---|---|
| 1 | 2 pprof, `testdata/profiles` | thoát 0; hotspot `slow.contains` (NDC 100%); 1 đường `BenchmarkDedup.func1 -> Dedup -> contains`; `report/` đủ hai tệp |
| 2 | benchmark LinearLookup | thoát 0; hotspot `CountMatches`; 1 đường; `paths_method` = paper |
| 3 | benchmark SortedInsert | thoát 0; hotspot `SortByInsertion`; 1 đường |
| 4 | Duplicates, `-rank-by raw` | thoát 1, `NO_POSITIVE_DELTA`, kèm gợi ý `perop` |
| 5 | Duplicates, mặc định | thoát 0; hotspot `Unique`; điểm đường gọi 0,0059 |
| 6 | sample/slow, `-attribute-runtime -hash-names -paths-method stack` | thoát 0; 3 hotspot; không còn chuỗi `slow` trong `report.json` |
| 7 | `-entry NoSuch -paths-method paper` | thoát 1, `ENTRY_NOT_COVERING` |
| 8 | `-entry NoSuch` (không đặt phương pháp) | thoát 0; cảnh báo rơi về `stack` |
| 9 | pprof hỏng (`junk.pprof`) | thoát 1, `NO_PROFILE`; chỉ có `run.json` và `diagnostics.json` |
| 10 | chạy lại với cùng `-out` | thành công; `report/` cũ được thay |
| 11 | 3 benchmark mẫu × 3 vòng, mặc định | 9/9 lần ra đúng hàm; điểm đường gọi dương và ổn định (LinearLookup 0,01159–0,01165; SortedInsert 0,01210–0,01214; Duplicates 0,00588–0,00593) |
| 12 | 3 benchmark mẫu, không đặt `-dir` (24/11, sau khi sửa lỗi) | thoát 0; mỗi benchmark 1 đường (điểm 0,0116; 0,0124; 0,0059) |

## 8. Khác với kế hoạch ban đầu

| Hạng mục | Kế hoạch / gói gốc | Thực tế | Lý do |
|---|---|---|---|
| Xếp hạng | giây thô (`raw`) | mặc định `perop` ở chế độ benchmark, `raw` vẫn dùng được | chạy thật cho thấy `raw` không ổn định ở benchmark (mục 5.1); nhóm quyết định 24/11 |
| Điểm đường gọi | giây thô | theo đơn vị của cách xếp hạng (`PathsOptions.BaseFactor`, `ScaledFactor`) | trước đó `Unique` có điểm −0,29 khi hotspot xếp theo thao tác; nay 0,0059 |
| `report.md` | chỉ cột giây thô | thêm cột "mỗi thao tác" khi hotspot có các trường `*_per_op_*` | hotspot hạng 1 từng hiện Δ âm và NDC 0% |
| Bộ đọc pprof | hai bộ (Long, Đức Anh) | một bộ: `pprofread`; xử lý đường dẫn Windows của Long chuyển vào `pprofread.RelFile` | tránh hai cách đọc cho cùng một tệp; test đối chiếu `go tool pprof` vẫn đạt nguyên |
| `-paths-method` | `paper` hoặc `stack` | thêm trạng thái chưa đặt: `paper`, rơi về `stack` khi `ENTRY_NOT_COVERING`; `run.json` ghi cái đã dùng | hợp đồng muốn ghi phương pháp thực sự |
| Cờ mới | | `-hash-names`, `-entry` | cần để nối báo cáo và đường gọi |
| Bộ lọc nhiễu | `runtime.*`, `testing.*` | thêm `internal/runtime/*` | đề xuất của Đức Anh |
| Cấu trúc gói | hai gói zip, mỗi gói một module Go riêng | gộp vào module `gofl` | đổi đường dẫn nhập, bỏ `replace`, dời fixture của Hiếu vào `internal/report/testdata` |
| Kiểu dữ liệu | `types.go` của Đức Anh khai lại `Hotspot`, `Error` | bỏ, giữ kiểu của Long, thêm `pathtypes.go` | tránh hai định nghĩa trùng |
| Thư mục `report/` | `report.Generate` từ chối thư mục đã có | bước report xóa `out/report` cũ trước khi ghi | chạy lại với cùng `-out` phải thành công |
| Đóng gói | `scripts/package.go`, `gofl-report`, `bin/` của Hiếu | `scripts/build.go` mới và `docs/RUNBOOK.md` mới | `gofl-report` chỉ lặp lại bước report đã nằm trong `gofl perf`; `package.go` chỉ build `gofl-report` và dùng module cũ |
| **Lỗi phát hiện khi viết báo cáo** | | **sửa 24/11**: không đặt `-dir` thì bước paths nhận thư mục rỗng, không khớp tên tệp, `critical_paths.json` là `[]` | mọi test trước đó đều truyền `-dir`. Nay thư mục dự án được chuẩn hóa một lần trong `options()` (`cmd/gofl/main.go`), có test `TestPerfBenchModeWithoutDirFindsPaths` |
