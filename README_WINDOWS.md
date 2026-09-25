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

The JSON schemas match macOS. An overview scan reports `"overview": true` with `"path": "/"` as a platform-neutral marker; each entry carries its real Windows path.

In `analyze`, arrow keys navigate, `Enter` opens a folder, `Space` selects, `Delete`/`Backspace` moves the selection to the Recycle Bin after you confirm with `Enter`, `O` opens with the default app, `F` shows it in File Explorer, `/` filters, `q` quits.

## Safety

- Deletes go to the Recycle Bin only; Mole never deletes permanently on its own. Paths on removable, network, or RAM volumes are refused, including items inside a folder where such a volume is mounted, or reached through a junction or symlink that leads to one (a link itself is recycled, not its target), because Windows would delete them permanently. If an item is too large for the Recycle Bin, Windows itself asks before deleting it permanently; answer No to keep it.
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

JSON şemaları macOS ile aynıdır. Genel bakış taraması platformdan bağımsız bir işaret olarak `"overview": true` ve `"path": "/"` döndürür; her girdide gerçek Windows yolu bulunur.

`analyze` içinde ok tuşları gezinir, `Enter` klasöre girer, `Space` seçer, `Delete`/`Backspace` seçimi `Enter` ile onayladıktan sonra Geri Dönüşüm Kutusu'na taşır, `O` varsayılan uygulamayla açar, `F` Dosya Gezgini'nde gösterir, `/` filtreler, `q` çıkar.

## Güvenlik

- Silme işlemleri yalnızca Geri Dönüşüm Kutusu'na gider; Mole kendi başına asla kalıcı silme yapmaz. Çıkarılabilir, ağ veya RAM birimlerindeki yollar reddedilir; böyle bir birimin bağlandığı klasördeki veya ona giden bir junction ya da sembolik bağlantı üzerinden erişilen öğeler de buna dahildir (bağlantının kendisi geri dönüşüme gider, hedefi değil), çünkü Windows bunları kalıcı olarak siler. Bir öğe Geri Dönüşüm Kutusu için fazla büyükse, kalıcı silmeden önce Windows'un kendisi sorar; öğeyi korumak için Hayır deyin.
- Sistem konumları korunur ve hiçbir şeye dokunulmadan reddedilir: sürücü kökleri ve sistem girdileri (`pagefile.sys`, `$Recycle.Bin`, `System Volume Information`, ...), `C:\Windows`, `Program Files`, `ProgramData` sistem klasörleri ve MSI önbellekleri, `C:\Users` ve tüm profil kökleri, ayrıca kendi `AppData`, `Temp` ve `OneDrive` kökleriniz. Kontroller büyük/küçük harfe duyarsızdır; junction ve 8.3 kısa adları da izlenir.
- UNC yolları (`\\sunucu\paylasim`), aygıt yolları (`\\?\`) ve alternatif veri akışları reddedilir.
