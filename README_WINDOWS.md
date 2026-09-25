# Mole for Windows

Mole on Windows is a single `mole.exe` with the two Go commands of the macOS CLI:

| Command | What it does |
| --- | --- |
| `mole analyze [PATH]` | Disk explorer TUI. Sizes folders, lists large files, and moves selections to the **Recycle Bin**. `--json` prints the same scan as JSON. |
| `mole status` | Live system health dashboard: CPU, memory, disks, network, battery, top processes. `--json` prints one snapshot, `--watch` streams NDJSON. |

`clean`, `uninstall`, `purge`, `optimize`, and the other shell commands are macOS-only for now. `mole.exe` says so instead of pretending to run them.

## Build

Requires Go (the version in `go.mod`). From a checkout of this repository:

```powershell
go build -o mole.exe ./cmd/mole
.\mole.exe --version
```

Or cross-compile both 64-bit targets from macOS or Linux:

```bash
make build-windows   # bin/mole-windows-amd64.exe, bin/mole-windows-arm64.exe
```

Rename the file to `mole.exe` and put it in a folder on your `PATH`. Windows Terminal renders the TUI best; the classic console host works but draws some icons as boxes.

## Usage

```powershell
mole analyze                    # overview: Home, AppData, Program Files, Windows, caches
mole analyze D:\Projects        # scan one folder
mole analyze --json $env:TEMP   # machine-readable
mole status                     # dashboard (q to quit)
mole status --json              # one JSON snapshot
mole status --watch --interval 2s
```

In `analyze`, arrow keys navigate, `Enter` opens a folder, `Space` selects, `Delete`/`Backspace` moves the selection to the Recycle Bin after you confirm with `Enter`, `O` opens with the default app, `F` shows it in File Explorer, `/` filters, `q` quits.

## Safety

- Deletes go to the Recycle Bin only. There is no permanent-delete fallback. Paths on removable, network, or RAM drives are refused, because Windows would delete them permanently.
- System locations are protected and refused before anything is touched: drive roots and their system entries (`pagefile.sys`, `$Recycle.Bin`, `System Volume Information`, ...), `C:\Windows`, `Program Files`, `ProgramData` system folders and MSI caches, `C:\Users` and every profile root, and your own `AppData`, `Temp`, and `OneDrive` roots. Checks are case-insensitive and follow junctions and 8.3 short names.
- UNC paths (`\\server\share`), device paths (`\\?\`), and alternate data streams are refused.

---

# Windows için Mole

Windows'ta Mole, macOS CLI'daki iki Go komutunu tek bir `mole.exe` içinde sunar:

| Komut | Ne yapar |
| --- | --- |
| `mole analyze [YOL]` | Disk gezgini TUI. Klasör boyutlarını ölçer, büyük dosyaları listeler ve seçimleri **Geri Dönüşüm Kutusu**'na taşır. `--json` aynı taramayı JSON olarak verir. |
| `mole status` | Canlı sistem sağlığı paneli: CPU, bellek, diskler, ağ, pil, en çok kaynak kullanan işlemler. `--json` tek bir anlık görüntü, `--watch` NDJSON akışı verir. |

`clean`, `uninstall`, `purge`, `optimize` ve diğer kabuk komutları şimdilik yalnızca macOS'ta. `mole.exe` bunları çalıştırıyormuş gibi yapmaz, desteklenmediğini söyler.

## Derleme

`go.mod`'daki Go sürümü gerekir. Depo kökünde:

```powershell
go build -o mole.exe ./cmd/mole
.\mole.exe --version
```

macOS veya Linux'tan iki 64-bit hedefi birden derlemek için:

```bash
make build-windows   # bin/mole-windows-amd64.exe, bin/mole-windows-arm64.exe
```

Dosyayı `mole.exe` olarak yeniden adlandırıp `PATH` içindeki bir klasöre koyun. TUI en iyi Windows Terminal'de görünür; klasik konsolda bazı simgeler kutu olarak çizilebilir.

## Kullanım

```powershell
mole analyze                    # genel bakış: Home, AppData, Program Files, Windows, önbellekler
mole analyze D:\Projeler        # tek bir klasörü tara
mole analyze --json $env:TEMP   # makine tarafından okunabilir çıktı
mole status                     # panel (çıkmak için q)
mole status --json              # tek JSON anlık görüntü
mole status --watch --interval 2s
```

`analyze` içinde ok tuşları gezinir, `Enter` klasöre girer, `Space` seçer, `Delete`/`Backspace` seçimi `Enter` ile onayladıktan sonra Geri Dönüşüm Kutusu'na taşır, `O` varsayılan uygulamayla açar, `F` Dosya Gezgini'nde gösterir, `/` filtreler, `q` çıkar.

## Güvenlik

- Silme işlemleri yalnızca Geri Dönüşüm Kutusu'na gider; kalıcı silmeye geri düşülmez. Çıkarılabilir, ağ veya RAM sürücülerindeki yollar reddedilir, çünkü Windows bunları kalıcı olarak siler.
- Sistem konumları korunur ve hiçbir şeye dokunulmadan reddedilir: sürücü kökleri ve sistem girdileri (`pagefile.sys`, `$Recycle.Bin`, `System Volume Information`, ...), `C:\Windows`, `Program Files`, `ProgramData` sistem klasörleri ve MSI önbellekleri, `C:\Users` ve tüm profil kökleri, ayrıca kendi `AppData`, `Temp` ve `OneDrive` kökleriniz. Kontroller büyük/küçük harfe duyarsızdır; junction ve 8.3 kısa adları da izlenir.
- UNC yolları (`\\sunucu\paylasim`), aygıt yolları (`\\?\`) ve alternatif veri akışları reddedilir.
