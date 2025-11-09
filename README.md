# Map Generator

Bu depo, karo tabanlı yoğunluk haritaları üretmek için yazılmış Go tabanlı bir HTTP sunucusu içerir. İstemciler, `POST /generate` uç noktasına gönderdikleri JSON tanımlarıyla otomatik olarak yerleştirilen karo kümelerinden oluşan PNG haritaları elde eder.

## Özellikler
- Karo boyutları ve adetleri için serbest biçimli tanım (`2x2*400,1x1*100` vb.)
- Dört farklı dağılım modu: `merkez`, `agirlik`, `adalar`, `iki-kita`
- Yüzük (ring) yapıları, ada kümeleri ve rastgele tohum (seed) desteği
- Yerleşim kapasiteleri, döndürme seçenekleri ve logaritmik tonlama ile ince ayar
- Sağlık kontrolü (`GET /healthz`) ve JSON tabanlı hata mesajları

## Başlangıç

### Gereksinimler
- Go 1.25.4 veya üstü

### Çalıştırma
```sh
go run .
```
Sunucu varsayılan olarak `http://127.0.0.1:8080` adresinde dinler. Dilerseniz ikili dosya oluşturup dağıtabilirsiniz:
```sh
go build -o map-generator.exe
./map-generator.exe
```

## API

### Uç Noktalar
- `GET /` – Basit yönlendirme mesajı döner
- `GET /healthz` – `{ "status": "ok" }` yanıtı verir
- `POST /generate` – İstek parametrelerine göre PNG (image/png) döndürür

### İstek Gövdesi
Aşağıdaki alanlardan gerek duyduklarınızı gönderin. Boş bırakılan alanlar için sunucu makul varsayılanlar seçer.

| Alan | Tip | Varsayılan | Açıklama |
| --- | --- | --- | --- |
| `w` | int | 512 | Harita genişliği (piksel) |
| `h` | int | 512 | Harita yüksekliği (piksel) |
| `tiles` | string | `2x2*400,2x1*300,1x1*100` | `WxH*Count` biçiminde karo listesi |
| `ka` | float | 1.0 | Toplam karo adetlerini ölçekler (0 ⇒ kapalı) |
| `cap` | int | 0 | Toplam yerleşim üst sınırı (0 ⇒ sınırsız) |
| `mode` | string | `agirlik` | Dağılım modu (`merkez`, `agirlik`, `adalar`, `iki-kita`) |
| `rings` | int | 3 | `merkez` modunda halka sayısı |
| `ringStart` | float | 0.1 | İç halkanın başlangıç yarıçapı (0–1 arası) |
| `ringEnd` | float | 0.8 | Dış halkanın bitiş yarıçapı (0–1 arası) |
| `seed` | string | Sistem zamanı | Rastgelelik tohumu |
| `logTone` | int | 1 | 0 ⇒ lineer, 1 ⇒ logaritmik tonlama |
| `brownCap` | int | 8 | Kahverengi tonuna geçiş için eşik |
| `bgA` | int | 0 | Arka plan alfa değeri (0–255) |
| `islands` | int | 4 | `adalar` modunda ada sayısı |
| `islandRFrac` | float | 0.25 | Ada yarıçapını belirleyen oran |
| `rot` | int | 1 | 0 ⇒ döndürme kapalı, 1 ⇒ karo döndürme açık |
| `n22` | int | 0 | Eski 2x2 karo sayısı (legacy) |
| `n21` | int | 0 | Eski 2x1 karo sayısı |
| `n11` | int | 0 | Eski 1x1 karo sayısı |

### Karo Listesi Biçimi
`tiles` alanı, virgülle ayrılmış `Genişlik x Yükseklik * Adet` parçalarından oluşur. Örnek: `2x2*400,2x1*300,1x1*100`. Adet değeri atlanırsa 1 kabul edilir. Negatif ya da sıfır değerler yok sayılır.

### Örnek İstek
```http
POST http://127.0.0.1:8080/generate
Content-Type: application/json
Accept: image/png

{
  "w": 384,
  "h": 256,
  "tiles": "1x1*100,2x1*150,3x3*40",
  "ka": 1.5,
  "cap": 600,
  "mode": "agirlik",
  "rot": 0
}
```
Sunucu, PNG verisini doğrudan yanıt gövdesinde döndürür. Başlıklarda kullanılan toplam karo sayısı (`X-Tile-Count`), parti sayısı (`X-Tile-Batches`) ve kullanılan tohum (`X-Seed`) bilgilerini bulabilirsiniz.

## Geliştirme
- Kod tek dosyada (`main.go`) bulunduğu için değişiklik sonrası `go run .` ile hızlıca test edilebilir.
- Yeni örnek istekler eklemek için `examples/requests.http` dosyasını kullanabilirsiniz.

## Lisans
Lisans bilgisi eklenmemiştir. Kullanım koşulları için depo sahibine danışın.
