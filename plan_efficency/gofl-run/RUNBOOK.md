# Chạy `gofl perf` (Nhóm E)

Tìm hàm làm chương trình Go chậm đi khi tải tăng. Chạy trên máy của bạn, không gửi gì ra ngoài. Phiên bản 0.1.0.

## Chuẩn bị

1. Chọn tệp trong `bin/` đúng máy: `gofl-linux-amd64`, `gofl-linux-arm64`, `gofl-darwin-amd64` (Mac Intel), `gofl-darwin-arm64` (Mac Apple), `gofl-windows-amd64.exe`, `gofl-windows-arm64.exe`. Đổi tên thành `gofl` (hoặc dùng nguyên tên) và đặt vào `PATH`.
   - Linux/macOS: `chmod +x gofl`. Mac chặn tệp tải về: `xattr -d com.apple.quarantine gofl`.
2. Chế độ benchmark cần `go` trên máy (cùng bản mà dự án dùng). Chế độ 2 pprof không cần Go.
3. Thử nhanh bằng dữ liệu mẫu: `gofl perf -base src/testdata/profiles/dedup_base.pprof -scaled src/testdata/profiles/dedup_scaled.pprof`. Phải thấy `status: ok` và mã thoát 0. Cảnh báo "about equally long" ở ví dụ này là bình thường.

## Chạy trên dự án

Mở terminal tại thư mục dự án (nơi có `go.mod`).

```
gofl preflight -bench BenchmarkTên -sizes 100,10000     # kiểm tra máy, không chứa mã
gofl perf -bench BenchmarkTên -sizes 100,10000          # chạy benchmark ở hai kích thước
gofl perf -base a.pprof -scaled b.pprof                 # hoặc: đã có sẵn hai CPU profile
```

| Tình huống | Làm gì |
|---|---|
| Dự án chưa có benchmark | Chép `src/docs/bench_template_test.go.txt` thành `xxx_test.go` cạnh mã cần đo, đổi tên package, điền chỗ TODO, bỏ `b.Skip`. Benchmark cần có một nhánh cho mỗi kích thước (`b.Run("size=100", ...)`). |
| Cần dựng DB/dịch vụ trước | Chép `src/gofl.example.yaml` thành `gofl.yaml` ở gốc dự án, điền `setup:` và `env:`. |
| Benchmark nằm trong một gói cụ thể | Thêm `-pkg ./đường/dẫn`. |
| Muốn tên hàm, tệp không lộ ra | Thêm `-hash-names` (chỉ ảnh hưởng `report.md`, `report.json`). |

Mọi kết quả nằm trong thư mục `out/` (đổi bằng `-out`).

Lưu ý khi viết benchmark: giữ phần chuẩn bị trước `b.ResetTimer()` (dựng dữ liệu, đọc tệp) ngắn, vì phần này không được đo nhưng vẫn lọt vào profile, làm số giây mỗi thao tác bị tính thấp đi (giải thích kèm ví dụ ở mục 5.1 của `BAO_CAO_VA_HUONG_DAN_GOFL.md`).

## Gửi gì lại

| Mức | Tệp | Chứa tên hàm, tệp? |
|---|---|---|
| 0 | `out/diagnostics.json` | Không. Chỉ trạng thái, mã lỗi, số mẫu, thời gian. |
| 1 | `out/report/report.md`, `out/report/report.json` | Có, trừ khi chạy với `-hash-names`. Đọc lại trước khi gửi. |

`hotspots.json`, `critical_paths.json`, `run.json`, `*.pprof` và các log trong `out/` có tên và đường dẫn thật: **đừng gửi** nếu chưa kiểm tra.

## Mã thoát và lỗi

- `0`: xong. `1`: chạy lỗi, mã lỗi nằm trong `out/diagnostics.json` (ví dụ `BUILD_FAIL` biên dịch lỗi, `NO_PROFILE` một kích thước không đủ mẫu, `NO_POSITIVE_DELTA` không hàm nào chậm hơn). `2`: sai cờ hoặc `gofl.yaml`.
- `NO_PROFILE`: tăng `-benchtime` (ví dụ `3s`) hoặc làm benchmark nặng hơn ở kích thước nhỏ.
- Muốn biết cờ nào có: `gofl perf -h`.

## Tự build từ mã nguồn

Mã nguồn, test và dữ liệu mẫu nằm trong `src/`. Cần Go 1.26 trở lên và mạng ở lần build đầu (tải `github.com/google/pprof`).

```
cd src
go run scripts/build.go                    # 6 hệ điều hành/kiến trúc vào src/bin
go run scripts/build.go -only linux/amd64  # chỉ một đích
go run scripts/build.go -out ../bin-moi    # thư mục khác
go vet ./... && go test ./...              # kiểm tra mã, khoảng 1 phút (có chạy go test thật trên testdata/sample)
```

Hoặc một đích bằng lệnh Go thường: `go build -trimpath -o gofl ./cmd/gofl`. Cùng bản Go và cùng mã nguồn cho ra tệp giống hệt tệp trong `bin/` (đã kiểm với bản linux-amd64).
