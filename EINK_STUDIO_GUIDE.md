# E-Ink / 老舊 Kindle 專屬儀表板與版面配置器規格書 (`EINK_STUDIO_GUIDE.md`)

本文件說明 **`mqtt-switchboard`** 專為老舊 Kindle（如 Kindle 3/4/Paperwhite 早期 Experimental Browser）及各尺寸（6" ~ 20"）E-Ink 電子紙設計的 **伺服器端極簡渲染引擎 (`/lite`)** 與 **現代電腦端視覺化版面配置器 (E-Ink Layout Studio)**。

---

## 1. 架構設計：為什麼不會壓垮 OpenWrt 路由器？

```text
  [ 設定人員現代電腦 (PC Browser) ]                  [ 老舊 Kindle / E-Ink 設備 ]
   - 運行 E-Ink Layout Studio (JS)                    - 瀏覽器零 JS / 零現代 CSS 運算
   - 自由拖拉大框、抽選 B4/B5/C1/C6                   - 僅載入純 HTML <table> 標籤
   - 即時算繪黑白電子紙預覽畫布                       - 點擊按鈕使用純 HTTP GET/POST 302 跳轉
                 │                                                  │
                 │ POST /api/layout (儲存 JSON)                     │ GET /lite (< 0.5ms 字串組裝)
                 ▼                                                  ▼
  ┌──────────────────────────────────────────────────────────────────────────────────┐
  │                     OpenWrt 路由器 (`mqtt-switchboard`)                          │
  │  1. 記憶體常駐快取 `EinkLayoutConfig` 與自訂 MQTT Topic 歷史池 (最近 3 筆)        │
  │  2. 讀取 `/proc/net/arp` 自動解析區網 E-Ink 設備 MAC 位址 (免密碼白名單 + 審計)   │
  │  3. `strings.Builder` 極速拼接高對比黑白 HTML 表格，CPU 與 Flash 零額外負載       │
  └──────────────────────────────────────────────────────────────────────────────────┘
```

1. **設定與預覽 100% 在設定電腦上運算**：
   - 當管理員在一般現代電腦打開 `http://<router-ip>:8080/` 並點擊右上角 **`[🛠️ E-Ink 版面配置器]`** 時，所有的排版編輯與右側「即時黑白電子紙預覽畫布」完全由電腦端瀏覽器繪製。
   - OpenWrt 路由器只在按下「儲存」時寫入一次 `/etc/mqtt-switchboard/layout.json`。
2. **E-Ink 顯示端 100% 伺服器端極簡渲染 (SSR)**：
   - 老舊 Kindle 訪問 `http://<router-ip>:8080/lite` 時，伺服器直接依配置組裝純 HTML `<table>`，無須跑 JavaScript 即可顯示與控制，極致省電。
3. **嚴格區網限制 (LAN-Only)**：
   - `/lite` 路由強制透過 `auth.IsPrivateOrLocalIP` 檢查來源 IP，任何來自外網 (WAN) 的連線一律回傳 `403 Forbidden`。

---

## 2. 核心功能與版面結構

### 2.1 WRT 底層 ARP 硬體 MAC 識別與免登入白名單
- 路由器直接讀取 Linux 核心記憶體表 `/proc/net/arp`，將連入 `/lite` 的區網 IP 自動對應出實體 MAC 位址（耗時 `< 0.05ms`）。
- **免登入直連**：只要該 Kindle 的 MAC 列於 `authorized_devices` 白名單（或啟用 `allow_all_lan`），進入 `/lite` 即可跳過帳密輸入框。
- **操作設備精準留痕**：當有人在某台 Kindle 點擊繼電器開關時，Syslog 會明確記錄是哪一台 E-Ink 設備觸發（例如：`雞舍大門_Kindle(MAC:AA:BB:CC:11:22:33)`）。
- 若跨網段無法讀取 ARP，亦支援在網址帶上 `?token=<專屬Token>&dev=<設備名稱>` 作為書籤直連。

### 2.2 頂部 MQTT 廣播 / 純文字看板區 (`top_banners`)
- 支援綁定任意自訂 MQTT Topic（純文字 Plain Text 或指定 `json_key` 萃取），例如：
  - 生活/工作呼叫廣播：`Farm/Broadcast/text`（如 `"開飯囉！請至餐廳集合"`）
  - 智慧孵蛋機事件回報：`Farm/Eggroller/RptMess`（如 `"Egg rolling finished (14s)"`）
- **保留最近 3 筆歷史紀錄**：
  - **最新第 1 筆**：以醒目大字粗體顯示（可在配置器選擇 `20px` ~ `46px`）。
  - **前 2~3 筆歷史**：以小字級（`12px` ~ `20px`）附帶接收時間戳排列於下方。

### 2.3 自定義複合大框 (`custom_boxes`)：跨設備抽選與雙數值組合
解決「A 大框包 B 設備與 C 設備，且只挑 B4, B5, C1, C6, C7 密集列出」以及「兩個內容組合成同一格」的需求：

1. **雙資料槽組合 (`Primary` + `Secondary`)**：
   - 每個數據格（Metric Cell）可單獨顯示一個來源，或點選 **「➕ 組合第2數值」** 將兩個不同來源用 `/` 合併在同一格：
   - 範例 **`[育雛室: 32.5°C / 目標 33.0°C]`**：
     - 格子標題：`育雛室`
     - **主數值 (`primary`)**：選擇設備實體或 Topic `Farm/Brooder/status` (JSON key: `temp`, 單位: `°C`)
     - **副數值 (`secondary`)**：手寫 Topic `Farm/Brooder/target` (JSON key: `temp`, 前綴: `目標 `, 單位: `°C`)
2. **跨設備控制項抽選 (`controls`)**：
   - 可從 `Mushi_8CH_RelayHub`、`ESP_Eggroller` 等任意設備中，只勾選需要的通道（如 `B4`, `B5`, `C6`, `C7`）放入同一個大框中，並可自訂每列排幾格（`metric_cols`: 1~6 格、`control_cols`: 1~4 格）。

### 2.4 設備斷線 / 數據逾時視覺防呆（`<del>` 刪除線 + 對角斜線背景）
- 為避免黑底白字反白與「開關開啟 (`ON ●`)」混淆，當某項感測器或設備超過設定的 `stale_timeout_minutes`（預設 15 分鐘）未更新或離線時：
  1. 數值與名稱自動套用 HTML 原生 **`<del>刪除線</del>`**（連無 CSS 模式都能正常顯示刪除線）。
  2. 表格儲存格套用 **135° 對角斜線背景紋路 + 粗虛線邊框** 並標上 `[逾時]` 標籤。

### 2.5 單頁不翻頁上限保護 (`max_unassigned_items`)
- E-Ink 儀表板不適合翻頁滾動。配置器可設定單頁未分組物件顯示上限（提供 `15` / `30` / `50` / `100` 件，適應 6 吋閱讀器至 10"、20" 大型電子紙看板）。
- 已放入自定義大框的實體不會在下方重複出現；超出單頁上限的未分組物件會自動隱藏（但在電腦設定端仍可隨時看見並勾選）。

---

## 3. `/lite` 網址參數一覽

| 參數 | 範例 | 說明 |
| :--- | :--- | :--- |
| `clean=1` | `/lite?clean=1` | **故意刷屏消殘影**：先顯示全黑畫面 1 秒 $\rightarrow$ 自動跳轉 `clean=2` 全白畫面 1 秒 $\rightarrow$ 跳回 `/lite` 主畫面。 |
| `refresh=N` | `/lite?refresh=60` | 覆寫 HTML `<meta http-equiv="refresh">` 定時刷新秒數（`0` 為停用定時刷新）。 |
| `nostyle=1` | `/lite?nostyle=1` | **極限無 CSS 模式**：移除 `<style>` 區塊，完全使用 HTML `<table border="1" bgcolor="#000000">` 與 `<font>` 繪製。 |
| `rot=90` | `/lite?rot=90` | 畫面旋轉 `90` 或 `270` 度（橫向擺放電子紙時使用）。 |
| `width=px` | `/lite?width=600px` | 限制頁面最大像素寬度。 |
| `wake=1` | `/lite?wake=1` | 於夜間休眠時段（18:00 - 08:00）強制臨時喚醒面板。 |
| `token=...` | `/lite?token=abc&dev=K1`| 跨網段無 ARP 時的免登入憑證與設備名稱標記。 |

---

## 4. 設定檔結構範例 (`/etc/mqtt-switchboard/layout.json`)

```json
{
  "order": ["eggroller_dev", "SmartRelay_FACD"],
  "global": {
    "pure_no_css": false,
    "default_refresh_sec": 60,
    "night_rest_enabled": true,
    "night_rest_start_hour": 18,
    "night_rest_end_hour": 8,
    "stale_timeout_minutes": 15,
    "max_unassigned_items": 30,
    "show_unassigned": true,
    "allow_all_lan": false
  },
  "authorized_devices": [
    {
      "mac": "AA:BB:CC:11:22:33",
      "name": "孵蛋室_Kindle4",
      "token": ""
    }
  ],
  "top_banners": [
    {
      "id": "banner_broadcast",
      "label": "園區廣播",
      "topic": "Farm/Broadcast/text",
      "json_key": "",
      "latest_size": "32px",
      "history_size": "14px",
      "max_history": 3
    },
    {
      "id": "banner_egg",
      "label": "孵蛋機事件",
      "topic": "Farm/Eggroller/RptMess",
      "json_key": "",
      "latest_size": "24px",
      "history_size": "14px",
      "max_history": 3
    }
  ],
  "custom_boxes": [
    {
      "id": "box_a",
      "title": "A：孵蛋與育雛重點監控",
      "subtitle": "跨設備密集排列",
      "metric_cols": 3,
      "control_cols": 2,
      "metrics": [
        {
          "id": "m_brooder",
          "label": "育雛室(現況/目標)",
          "primary": {
            "source_type": "topic",
            "topic": "Farm/Brooder/status",
            "json_key": "temp",
            "prefix": "",
            "unit": "°C"
          },
          "secondary": {
            "source_type": "topic",
            "topic": "Farm/Brooder/target",
            "json_key": "temp",
            "prefix": "目標 ",
            "unit": "°C"
          }
        },
        {
          "id": "m_egg_th",
          "label": "孵蛋箱溫濕度",
          "primary": {
            "source_type": "topic",
            "topic": "Farm/Eggroller/status",
            "json_key": "temp",
            "prefix": "",
            "unit": "°C"
          },
          "secondary": {
            "source_type": "topic",
            "topic": "Farm/Eggroller/status",
            "json_key": "hum",
            "prefix": "",
            "unit": "%"
          }
        },
        {
          "id": "m_egg_day",
          "label": "孵化進度",
          "primary": {
            "source_type": "topic",
            "topic": "Farm/Eggroller/status",
            "json_key": "mode",
            "prefix": "",
            "unit": ""
          },
          "secondary": {
            "source_type": "topic",
            "topic": "Farm/Eggroller/status",
            "json_key": "day",
            "prefix": "Day ",
            "unit": ""
          }
        }
      ],
      "controls": [
        { "entity_id": "SmartRelay_FACD_CH4", "alias": "B4 排風扇" },
        { "entity_id": "SmartRelay_FACD_CH5", "alias": "B5 補光燈" },
        { "entity_id": "eggroller_sw_heater", "alias": "C6 保溫加熱燈" },
        { "entity_id": "eggroller_sw_roller", "alias": "C7 手動翻蛋" }
      ]
    }
  ],
  "hidden_unassigned": []
}
```

