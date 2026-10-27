package server

import (
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"

	"github.com/your-repo/mqtt-switchboard/internal/auth"
	"github.com/your-repo/mqtt-switchboard/internal/discovery"
)

func (s *Server) handleLite(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	var username string
	if err == nil && cookie.Value != "" {
		if sess, ok := s.authMgr.ValidateSession(cookie.Value); ok {
			username = sess.Username
		}
	}

	if username == "" {
		s.renderLiteLogin(w, r, "")
		return
	}

	s.renderLiteDashboard(w, r, username)
}

func (s *Server) handleLiteLogin(w http.ResponseWriter, r *http.Request) {
	clientIP := auth.GetRealIP(r)

	if locked, _ := s.authMgr.IsIPLocked(clientIP); locked {
		s.logger.AuthFail("unknown", clientIP, "IP_LOCKED_15M")
		s.renderLiteLogin(w, r, "登入失敗次數過多，IP 已暫時鎖定 15 分鐘。")
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderLiteLogin(w, r, "請求格式錯誤")
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	if !s.cfg.Authenticate(username, password) {
		isLocked := s.authMgr.RecordLoginFailure(clientIP)
		if isLocked {
			s.logger.AuthFail(username, clientIP, "EXCEEDED_MAX_ATTEMPTS_LOCKED")
			s.renderLiteLogin(w, r, "登入失敗次數過多，IP 已鎖定 15 分鐘。")
		} else {
			s.logger.AuthFail(username, clientIP, "INVALID_CREDENTIALS")
			s.renderLiteLogin(w, r, "帳號或密碼錯誤，請重新輸入。")
		}
		return
	}

	s.authMgr.RecordLoginSuccess(clientIP)
	token, err := s.authMgr.CreateSession(username)
	if err != nil {
		s.renderLiteLogin(w, r, "建立連線階段失敗")
		return
	}

	maxAge := s.cfg.Server.SessionTimeoutDays * 86400
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.Server.CookieSecure,
	})

	http.Redirect(w, r, "/lite", http.StatusSeeOther)
}

func (s *Server) handleLiteLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.authMgr.RevokeSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.Server.CookieSecure,
	})
	http.Redirect(w, r, "/lite", http.StatusSeeOther)
}

func (s *Server) handleLiteAction(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		http.Redirect(w, r, "/lite", http.StatusSeeOther)
		return
	}
	sess, ok := s.authMgr.ValidateSession(cookie.Value)
	if !ok {
		http.Redirect(w, r, "/lite", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/lite", http.StatusSeeOther)
		return
	}

	deviceID := r.FormValue("device_id")
	action := r.FormValue("action")
	if action == "TOGGLE" {
		action = ""
	}

	clientIP := auth.GetRealIP(r)
	_ = s.mqttCli.TriggerDevice(deviceID, action, sess.Username, clientIP)

	if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
		return
	}

	http.Redirect(w, r, "/lite", http.StatusSeeOther)
}

func (s *Server) renderLiteLogin(w http.ResponseWriter, r *http.Request, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	errBox := ""
	if errMsg != "" {
		errBox = fmt.Sprintf(`<div class="err">%s</div>`, html.EscapeString(errMsg))
	}

	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Kindle 登入 - MQTT Switchboard</title>
  <style>
    body { font-family: sans-serif; background: #ffffff; color: #000000; padding: 20px; max-width: 420px; margin: 0 auto; line-height: 1.4; }
    h1 { font-size: 1.3em; border-bottom: 3px solid #000000; padding-bottom: 8px; margin-bottom: 16px; }
    .err { border: 2px solid #000000; padding: 10px; margin-bottom: 16px; font-weight: bold; background: #ffffff; }
    .field { margin-bottom: 14px; }
    label { display: block; font-weight: bold; margin-bottom: 4px; font-size: 0.95em; }
    input[type="text"], input[type="password"] { width: 100%%; padding: 10px; font-size: 16px; border: 2px solid #000000; box-sizing: border-box; }
    input[type="submit"] { background: #000000; color: #ffffff; border: 2px solid #000000; padding: 12px; font-size: 17px; font-weight: bold; width: 100%%; cursor: pointer; margin-top: 10px; }
    .links { margin-top: 25px; font-size: 0.85em; text-align: center; }
    a { color: #000000; font-weight: bold; }
  </style>
</head>
<body>
  <h1>MQTT Switchboard (Kindle 極簡版)</h1>
  %s
  <form action="/lite/login" method="POST">
    <div class="field">
      <label for="u">使用者帳號 Username:</label>
      <input type="text" id="u" name="username" autofocus autocomplete="username" autocapitalize="none">
    </div>
    <div class="field">
      <label for="p">登入密碼 Password:</label>
      <input type="password" id="p" name="password" autocomplete="current-password">
    </div>
    <input type="submit" value="登 入">
  </form>
  <div class="links">
    <a href="/">切換回現代色彩版 (Modern UI)</a>
  </div>
</body>
</html>`, errBox)

	_, _ = w.Write([]byte(htmlContent))
}

type liteGroup struct {
	ID           string
	Name         string
	Model        string
	Manufacturer string
	Area         string
	RSSI         string
	Metrics      []discovery.DeviceEntity
	Controls     []discovery.DeviceEntity
}

func (s *Server) renderLiteDashboard(w http.ResponseWriter, r *http.Request, username string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	allDevices := s.registry.GetAll()

	// Group devices by device_id
	groupsMap := make(map[string]*liteGroup)
	for _, dev := range allDevices {
		devID := dev.DeviceID
		if devID == "" {
			devID = "standalone"
		}
		grp, exists := groupsMap[devID]
		if !exists {
			devName := dev.DeviceName
			if devName == "" {
				devName = devID
			}
			grp = &liteGroup{
				ID:           devID,
				Name:         devName,
				Model:        dev.DeviceModel,
				Manufacturer: dev.Manufacturer,
				Area:         dev.DeviceArea,
			}
			groupsMap[devID] = grp
		}

		if grp.Manufacturer == "" && dev.Manufacturer != "" {
			grp.Manufacturer = dev.Manufacturer
		}
		if grp.Model == "" && dev.DeviceModel != "" {
			grp.Model = dev.DeviceModel
		}
		if grp.Area == "" && dev.DeviceArea != "" {
			grp.Area = dev.DeviceArea
		}

		// Check for RSSI
		nameLow := strings.ToLower(dev.Name)
		idLow := strings.ToLower(dev.ID)
		keyLow := strings.ToLower(dev.JSONKey)
		if strings.Contains(nameLow, "rssi") || strings.Contains(idLow, "rssi") || keyLow == "rssi" {
			if dev.State != "" && dev.State != "UNKNOWN" {
				grp.RSSI = dev.State + " dBm"
			}
			continue // Do not show RSSI as regular entity
		}

		// Filter out telemetry diagnostic items in Lite mode
		isDiag := false
		diagKeywords := []string{"wifi", "ssid", "heap", "uptime", "ip", "mac", "prefix", "phy", "sensor_type", "version", "firmware", "sw_ver"}
		for _, kw := range diagKeywords {
			if strings.Contains(nameLow, kw) || strings.Contains(idLow, kw) || keyLow == kw {
				isDiag = true
				break
			}
		}
		if isDiag {
			continue
		}

		// Separate metrics from controls
		if dev.Component == "sensor" || dev.Component == "binary_sensor" {
			grp.Metrics = append(grp.Metrics, dev)
		} else {
			grp.Controls = append(grp.Controls, dev)
		}
	}

	// Sort groups
	var groups []*liteGroup
	for _, g := range groupsMap {
		// Sort entities inside
		sort.Slice(g.Metrics, func(i, j int) bool {
			return g.Metrics[i].Name < g.Metrics[j].Name
		})
		sort.Slice(g.Controls, func(i, j int) bool {
			return g.Controls[i].Name < g.Controls[j].Name
		})
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Name < groups[j].Name
	})

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <meta http-equiv="refresh" content="60">
  <title>MQTT Switchboard (Kindle)</title>
  <style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body { font-family: sans-serif; background: #ffffff; color: #000000; padding: 10px; max-width: 640px; margin: 0 auto; line-height: 1.3; }
    .top-bar { border-bottom: 3px solid #000000; padding-bottom: 8px; margin-bottom: 12px; }
    .top-bar h1 { font-size: 1.2em; display: inline-block; vertical-align: middle; }
    .nav-links { float: right; font-size: 0.85em; }
    .nav-links a { color: #000000; font-weight: bold; text-decoration: none; border: 1px solid #000000; padding: 3px 6px; margin-left: 4px; }
    .clearfix { clear: both; }

    .card { border: 2px solid #000000; padding: 10px; margin-bottom: 14px; background: #ffffff; }
    .card-title { font-size: 1.1em; font-weight: bold; }
    .card-sub { font-size: 0.78em; color: #333333; margin-bottom: 8px; border-bottom: 1px solid #000000; padding-bottom: 4px; }
    .meta-tag { float: right; font-size: 0.75em; border: 1px solid #000000; padding: 1px 4px; font-weight: bold; }

    /* Metrics table for clean horizontal display */
    .metric-row { width: 100%; border-collapse: collapse; margin-bottom: 8px; }
    .metric-cell { border: 1px solid #000000; padding: 4px 6px; text-align: center; width: 33%; vertical-align: middle; }
    .metric-lbl { font-size: 0.72em; display: block; color: #333333; }
    .metric-val { font-size: 1.05em; font-weight: bold; font-family: monospace; }

    /* Control buttons grid */
    .btn-table { width: 100%; border-collapse: separate; border-spacing: 5px; }
    .btn-table td { width: 50%; vertical-align: top; }
    .ctrl-btn { width: 100%; min-height: 46px; font-size: 0.95em; font-weight: bold; padding: 8px; cursor: pointer; border: 2px solid #000000; }
    .ctrl-btn.on { background: #000000; color: #ffffff; }
    .ctrl-btn.off { background: #ffffff; color: #000000; }
    .btn-desc { display: block; font-size: 0.72em; font-weight: normal; margin-top: 2px; }

    .empty { padding: 20px; border: 2px dashed #000000; text-align: center; font-weight: bold; }
  </style>
</head>
<body>
  <div class="top-bar">
    <h1>MQTT Switchboard (Kindle)</h1>
    <div class="nav-links">
      <a href="/lite">重新整理 ↺</a>
      <a href="/lite/logout">登出</a>
      <a href="/">色彩版</a>
    </div>
    <div class="clearfix"></div>
  </div>
`)

	if len(groups) == 0 {
		b.WriteString(`<div class="empty">尚無設備上線。<br>請確認 MQTT Broker 連線中...</div>`)
	}

	for _, grp := range groups {
		subParts := []string{}
		if grp.Area != "" {
			subParts = append(subParts, html.EscapeString(grp.Area))
		}
		subParts = append(subParts, html.EscapeString(grp.ID))
		subtitleText := strings.Join(subParts, " · ")

		metaParts := []string{}
		if grp.RSSI != "" {
			metaParts = append(metaParts, "📶 "+html.EscapeString(grp.RSSI))
		}
		if grp.Manufacturer != "" {
			metaParts = append(metaParts, html.EscapeString(grp.Manufacturer))
		}
		if grp.Model != "" {
			metaParts = append(metaParts, html.EscapeString(grp.Model))
		}
		metaText := strings.Join(metaParts, " · ")

		b.WriteString(fmt.Sprintf(`
  <div class="card">
    <div>
      %s
      <div class="card-title">%s</div>
    </div>
    <div class="card-sub">%s</div>
`,
			func() string {
				if metaText != "" {
					return fmt.Sprintf(`<span class="meta-tag">%s</span>`, metaText)
				}
				return ""
			}(),
			html.EscapeString(grp.Name),
			subtitleText,
		))

		// Render Metrics
		if len(grp.Metrics) > 0 {
			b.WriteString(`<table class="metric-row"><tr>`)
			for i, m := range grp.Metrics {
				if i > 0 && i%3 == 0 {
					b.WriteString(`</tr><tr>`)
				}
				cleanName := cleanLiteName(m.Name, grp.Name, grp.Area)
				valStr := m.State
				if valStr == "" {
					valStr = "N/A"
				}
				if m.UnitOfMeasurement != "" {
					valStr += " " + m.UnitOfMeasurement
				}
				b.WriteString(fmt.Sprintf(`
      <td class="metric-cell">
        <span class="metric-lbl">%s</span>
        <span class="metric-val">%s</span>
      </td>`, html.EscapeString(cleanName), html.EscapeString(valStr)))
			}
			// Fill remainder cells in row
			rem := len(grp.Metrics) % 3
			if rem != 0 {
				for k := 0; k < (3 - rem); k++ {
					b.WriteString(`<td class="metric-cell" style="border:none;"></td>`)
				}
			}
			b.WriteString(`</tr></table>`)
		}

		// Render Controls
		if len(grp.Controls) > 0 {
			b.WriteString(`<table class="btn-table"><tr>`)
			for i, c := range grp.Controls {
				if i > 0 && i%2 == 0 {
					b.WriteString(`</tr><tr>`)
				}
				cleanName := cleanLiteName(c.Name, grp.Name, grp.Area)
				isOn := (c.State == "ON" || c.State == c.PayloadOn)
				btnClass := "off"
				stateLabel := "OFF ○"
				if isOn {
					btnClass = "on"
					stateLabel = "ON ●"
				}
				if c.Component == "button" {
					btnClass = "off"
					stateLabel = "觸發 ▶"
				}

				b.WriteString(fmt.Sprintf(`
      <td>
        <form action="/lite/action" method="POST" onsubmit="return act(this, '%s', 'TOGGLE');">
          <input type="hidden" name="device_id" value="%s">
          <input type="hidden" name="action" value="TOGGLE">
          <button type="submit" class="ctrl-btn %s">
            %s
            <span class="btn-desc">%s</span>
          </button>
        </form>
      </td>`,
					html.EscapeString(c.ID),
					html.EscapeString(c.ID),
					btnClass,
					html.EscapeString(cleanName),
					stateLabel,
				))
			}
			if len(grp.Controls)%2 != 0 {
				b.WriteString(`<td></td>`)
			}
			b.WriteString(`</tr></table>`)
		}

		b.WriteString(`  </div>`) // end card
	}

	b.WriteString(`
  <script>
    function act(formEl, id, actionVal) {
      try {
        var xhr = new XMLHttpRequest();
        xhr.open('POST', '/lite/action', true);
        xhr.setRequestHeader('Content-Type', 'application/x-www-form-urlencoded');
        xhr.setRequestHeader('X-Requested-With', 'XMLHttpRequest');
        xhr.onreadystatechange = function() {
          if (xhr.readyState === 4) {
            window.location.reload();
          }
        };
        xhr.send('device_id=' + encodeURIComponent(id) + '&action=' + encodeURIComponent(actionVal));
        return false;
      } catch (e) {
        return true;
      }
    }
  </script>
</body>
</html>`)

	_, _ = w.Write([]byte(b.String()))
}

func cleanLiteName(fullName, groupName, groupArea string) string {
	s := strings.TrimSpace(fullName)
	prefixes := []string{}
	if groupArea != "" && groupName != "" {
		prefixes = append(prefixes, groupArea+" "+groupName+" ")
		prefixes = append(prefixes, groupArea+"_"+groupName+"_")
		prefixes = append(prefixes, groupArea+" "+groupName+"_")
	}
	if groupName != "" {
		prefixes = append(prefixes, groupName+" ")
		prefixes = append(prefixes, groupName+"_")
	}
	if groupArea != "" {
		prefixes = append(prefixes, groupArea+" ")
		prefixes = append(prefixes, groupArea+"_")
	}
	for _, p := range prefixes {
		if strings.HasPrefix(strings.ToLower(s), strings.ToLower(p)) {
			s = strings.TrimSpace(s[len(p):])
			break
		}
	}
	if s == "" {
		return fullName
	}
	return s
}
