package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

type StatusResponse struct {
	Status       string `json:"status"` // "connected" or "disconnected"
	Database     string `json:"database,omitempty"`
	User         string `json:"user,omitempty"`
	ServerTime   string `json:"server_time,omitempty"`
	LatencyMs    int64  `json:"latency_ms"`
	PasswordHint string `json:"password_hint,omitempty"`
	PodName      string `json:"pod_name"`
	SecretFile   string `json:"secret_file"`
	Error        string `json:"error,omitempty"`
}

var (
	db      *sql.DB
	dbMu    sync.RWMutex
	lastPw  string
	lastErr error
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func readPasswordFromDisk() (string, error) {
	pwFile := getEnv("DB_PASSWORD_FILE", "/mnt/secrets/db-password/db-password")
	data, err := os.ReadFile(pwFile)
	if err != nil {
		if fallback := os.Getenv("DB_PASSWORD"); fallback != "" {
			return strings.TrimSpace(fallback), nil
		}
		return "", fmt.Errorf("failed to read password file %s: %w", pwFile, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func updateDBConnection(pw string) error {
	host := getEnv("DB_HOST", "postgres")
	port := getEnv("DB_PORT", "5432")
	user := getEnv("DB_USER", "postgres")
	dbname := getEnv("DB_NAME", "appdb")
	sslmode := getEnv("DB_SSLMODE", "disable")

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s connect_timeout=3",
		host, port, user, pw, dbname, sslmode)

	newDB, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("sql.Open failed: %w", err)
	}
	newDB.SetMaxOpenConns(5)
	newDB.SetMaxIdleConns(2)
	newDB.SetConnMaxLifetime(1 * time.Minute)

	dbMu.Lock()
	defer dbMu.Unlock()

	if db != nil {
		_ = db.Close()
	}
	db = newDB
	lastPw = pw
	lastErr = nil
	log.Printf("Updated in-memory database connection pool (target: %s:%s/%s, user: %s)", host, port, dbname, user)
	return nil
}

// startSecretWatcher periodically checks the mounted secret file in the background (asynchronously),
// eliminating per-request disk I/O bottlenecks while reacting to rotations.
func startSecretWatcher(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			pw, err := readPasswordFromDisk()
			if err != nil {
				dbMu.Lock()
				lastErr = err
				dbMu.Unlock()
				continue
			}

			dbMu.RLock()
			currentPw := lastPw
			dbMu.RUnlock()

			if pw != currentPw {
				log.Printf("Detected secret rotation in mounted volume; reloading database connection pool")
				if err := updateDBConnection(pw); err != nil {
					log.Printf("Failed to update connection pool on rotation: %v", err)
					dbMu.Lock()
					lastErr = err
					dbMu.Unlock()
				}
			}
		}
	}()
}

func getDB() (*sql.DB, string, error) {
	dbMu.RLock()
	defer dbMu.RUnlock()

	if db == nil {
		if lastErr != nil {
			return nil, "", lastErr
		}
		return nil, "", fmt.Errorf("database connection pool not initialized")
	}

	return db, lastPw, nil
}

func maskPassword(pw string) string {
	if len(pw) <= 4 {
		return "****"
	}
	return pw[:2] + strings.Repeat("*", len(pw)-4) + pw[len(pw)-2:]
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	podName, _ := os.Hostname()
	pwFile := getEnv("DB_PASSWORD_FILE", "/mnt/secrets/db-password/db-password")

	start := time.Now()
	conn, pw, err := getDB()
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(StatusResponse{
			Status:     "disconnected",
			PodName:    podName,
			SecretFile: pwFile,
			LatencyMs:  time.Since(start).Milliseconds(),
			Error:      err.Error(),
		})
		return
	}

	var serverTime, dbName, dbUser string
	queryErr := conn.QueryRowContext(r.Context(), "SELECT NOW()::text, current_database(), current_user;").Scan(&serverTime, &dbName, &dbUser)
	latency := time.Since(start).Milliseconds()

	if queryErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(StatusResponse{
			Status:       "disconnected",
			PodName:      podName,
			SecretFile:   pwFile,
			PasswordHint: maskPassword(pw),
			LatencyMs:    latency,
			Error:        fmt.Sprintf("Query failed: %v", queryErr),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(StatusResponse{
		Status:       "connected",
		Database:     dbName,
		User:         dbUser,
		ServerTime:   serverTime,
		LatencyMs:    latency,
		PasswordHint: maskPassword(pw),
		PodName:      podName,
		SecretFile:   pwFile,
	})
}

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Dynamic Secret Operator – Database Auto-Rotation</title>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600;700;800&family=JetBrains+Mono:wght@400;600&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg-color: #0b0f19;
            --card-bg: rgba(18, 24, 38, 0.7);
            --card-border: rgba(255, 255, 255, 0.08);
            --text-main: #f3f4f6;
            --text-muted: #9ca3af;
            --accent-green: #10b981;
            --accent-red: #ef4444;
            --accent-blue: #3b82f6;
            --glow-color: rgba(16, 185, 129, 0.15);
        }
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: 'Outfit', sans-serif;
            background-color: var(--bg-color);
            background-image: 
                radial-gradient(at 0% 0%, rgba(59, 130, 246, 0.12) 0px, transparent 50%),
                radial-gradient(at 100% 100%, rgba(16, 185, 129, 0.08) 0px, transparent 50%);
            min-height: 100vh;
            display: flex;
            flex-direction: column;
            align-items: center;
            justify-content: center;
            padding: 30px 20px;
            color: var(--text-main);
        }
        .container {
            width: 100%;
            max-width: 860px;
            display: flex;
            flex-direction: column;
            gap: 24px;
        }
        .header {
            text-align: center;
            margin-bottom: 8px;
        }
        .header h1 {
            font-size: 28px;
            font-weight: 800;
            letter-spacing: -0.5px;
            background: linear-gradient(135deg, #ffffff 0%, #9ca3af 100%);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
        }
        .header p {
            color: var(--text-muted);
            font-size: 14px;
            margin-top: 6px;
        }
        .status-hero {
            background: var(--card-bg);
            backdrop-filter: blur(20px);
            -webkit-backdrop-filter: blur(20px);
            border: 1px solid var(--card-border);
            border-radius: 24px;
            padding: 40px;
            text-align: center;
            box-shadow: 0 20px 40px rgba(0, 0, 0, 0.4);
            transition: all 0.5s cubic-bezier(0.16, 1, 0.3, 1);
            position: relative;
            overflow: hidden;
        }
        .status-hero.connected {
            border-color: rgba(16, 185, 129, 0.3);
            box-shadow: 0 20px 50px rgba(16, 185, 129, 0.15);
        }
        .status-hero.disconnected {
            border-color: rgba(239, 68, 68, 0.4);
            box-shadow: 0 20px 50px rgba(239, 68, 68, 0.2);
        }
        .indicator-ring {
            width: 90px;
            height: 90px;
            border-radius: 50%;
            margin: 0 auto 20px;
            display: flex;
            align-items: center;
            justify-content: center;
            transition: all 0.5s ease;
        }
        .connected .indicator-ring {
            background: rgba(16, 185, 129, 0.15);
            border: 2px solid var(--accent-green);
            box-shadow: 0 0 30px rgba(16, 185, 129, 0.4);
        }
        .disconnected .indicator-ring {
            background: rgba(239, 68, 68, 0.15);
            border: 2px solid var(--accent-red);
            box-shadow: 0 0 30px rgba(239, 68, 68, 0.4);
        }
        .status-title {
            font-size: 32px;
            font-weight: 800;
            letter-spacing: -0.5px;
            margin-bottom: 8px;
            text-transform: uppercase;
        }
        .connected .status-title { color: var(--accent-green); }
        .disconnected .status-title { color: var(--accent-red); }
        .status-desc {
            color: var(--text-muted);
            font-size: 15px;
        }
        .metrics-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
            gap: 16px;
        }
        .metric-card {
            background: var(--card-bg);
            backdrop-filter: blur(16px);
            border: 1px solid var(--card-border);
            border-radius: 18px;
            padding: 20px;
            display: flex;
            flex-direction: column;
            gap: 6px;
        }
        .metric-label {
            font-size: 11px;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 1px;
            color: var(--text-muted);
        }
        .metric-value {
            font-family: 'JetBrains Mono', monospace;
            font-size: 15px;
            font-weight: 600;
            color: var(--text-main);
            word-break: break-all;
        }
        .log-section {
            background: var(--card-bg);
            backdrop-filter: blur(16px);
            border: 1px solid var(--card-border);
            border-radius: 20px;
            padding: 24px;
            display: flex;
            flex-direction: column;
            gap: 12px;
        }
        .log-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        .log-header h3 {
            font-size: 14px;
            font-weight: 700;
            text-transform: uppercase;
            letter-spacing: 1px;
            color: var(--text-muted);
        }
        .live-badge {
            display: inline-flex;
            align-items: center;
            gap: 6px;
            font-size: 12px;
            font-weight: 600;
            color: var(--accent-blue);
            background: rgba(59, 130, 246, 0.1);
            padding: 4px 10px;
            border-radius: 20px;
        }
        .live-dot {
            width: 6px;
            height: 6px;
            background: var(--accent-blue);
            border-radius: 50%;
            animation: pulse 1.5s infinite;
        }
        .log-container {
            font-family: 'JetBrains Mono', monospace;
            font-size: 12px;
            max-height: 180px;
            overflow-y: auto;
            display: flex;
            flex-direction: column;
            gap: 6px;
            background: rgba(0, 0, 0, 0.3);
            border-radius: 12px;
            padding: 12px;
        }
        .log-entry {
            display: flex;
            gap: 12px;
            color: #d1d5db;
        }
        .log-time { color: #6b7280; }
        .log-success { color: var(--accent-green); }
        .log-error { color: var(--accent-red); }
        @keyframes pulse {
            0% { transform: scale(0.9); opacity: 0.6; }
            50% { transform: scale(1.2); opacity: 1; }
            100% { transform: scale(0.9); opacity: 0.6; }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>Dynamic Secret Operator – Database PoC</h1>
            <p>Demonstrating automated 5-minute database password rotation with zero application downtime.</p>
        </div>

        <div id="hero" class="status-hero connected">
            <div class="indicator-ring">
                <svg id="status-icon" width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"></path>
                    <polyline points="22 4 12 14.01 9 11.01"></polyline>
                </svg>
            </div>
            <div id="status-title" class="status-title">DATABASE CONNECTED</div>
            <div id="status-desc" class="status-desc">Active connection to PostgreSQL instance (SELECT 1 OK)</div>
        </div>

        <div class="metrics-grid">
            <div class="metric-card">
                <span class="metric-label">Database & User</span>
                <span id="metric-db" class="metric-value">postgres@appdb</span>
            </div>
            <div class="metric-card">
                <span class="metric-label">Active Secret Hint</span>
                <span id="metric-pw" class="metric-value">******</span>
            </div>
            <div class="metric-card">
                <span class="metric-label">Query Latency</span>
                <span id="metric-lat" class="metric-value">0 ms</span>
            </div>
            <div class="metric-card">
                <span class="metric-label">Serving Pod</span>
                <span id="metric-pod" class="metric-value">-</span>
            </div>
        </div>

        <div class="log-section">
            <div class="log-header">
                <h3>Live Health Audit Stream</h3>
                <span class="live-badge"><span class="live-dot"></span> Polling every 2s</span>
            </div>
            <div id="logs" class="log-container">
                <div class="log-entry"><span class="log-time">[Init]</span><span>Connecting to status stream...</span></div>
            </div>
        </div>
    </div>

    <script>
        let lastState = null;
        const hero = document.getElementById('hero');
        const statusTitle = document.getElementById('status-title');
        const statusDesc = document.getElementById('status-desc');
        const metricDb = document.getElementById('metric-db');
        const metricPw = document.getElementById('metric-pw');
        const metricLat = document.getElementById('metric-lat');
        const metricPod = document.getElementById('metric-pod');
        const logs = document.getElementById('logs');

        function addLog(type, msg) {
            const time = new Date().toLocaleTimeString();
            const entry = document.createElement('div');
            entry.className = 'log-entry';
            const typeClass = type === 'ok' ? 'log-success' : 'log-error';
            entry.innerHTML = '<span class="log-time">[' + time + ']</span><span class="' + typeClass + '">' + msg + '</span>';
            logs.insertBefore(entry, logs.firstChild);
            if (logs.children.length > 50) logs.removeChild(logs.lastChild);
        }

        async function pollStatus() {
            try {
                const res = await fetch('/api/status');
                const data = await res.json();

                metricLat.innerText = data.latency_ms + ' ms';
                metricPod.innerText = data.pod_name || '-';

                if (data.status === 'connected') {
                    hero.className = 'status-hero connected';
                    statusTitle.innerText = 'DATABASE CONNECTED';
                    statusDesc.innerText = 'Active PostgreSQL session: ' + (data.server_time || '');
                    metricDb.innerText = data.user + '@' + data.database;
                    metricPw.innerText = data.password_hint || '******';

                    if (lastState !== 'connected') {
                        addLog('ok', 'Connection established with secret revision (' + (data.password_hint || '') + ')');
                        lastState = 'connected';
                    }
                } else {
                    hero.className = 'status-hero disconnected';
                    statusTitle.innerText = 'DISCONNECTED / RECONNECTING';
                    statusDesc.innerText = data.error || 'Connection failed';

                    if (lastState !== 'disconnected') {
                        addLog('err', 'Connection error: ' + (data.error || 'Unknown error'));
                        lastState = 'disconnected';
                    }
                }
            } catch (e) {
                hero.className = 'status-hero disconnected';
                statusTitle.innerText = 'BACKEND UNREACHABLE';
                statusDesc.innerText = e.message;
                if (lastState !== 'unreachable') {
                    addLog('err', 'Failed to reach /api/status endpoint');
                    lastState = 'unreachable';
                }
            }
        }

        pollStatus();
        setInterval(pollStatus, 2000);
    </script>
</body>
</html>`

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

func main() {
	port := getEnv("PORT", "8080")

	// Load initial secret into memory at startup
	if initialPw, err := readPasswordFromDisk(); err == nil {
		if err := updateDBConnection(initialPw); err != nil {
			log.Printf("Warning: initial DB connection setup failed: %v", err)
		}
	} else {
		log.Printf("Warning: initial password read failed: %v", err)
	}

	// Start asynchronous background secret watcher (polling every 1 second)
	startSecretWatcher(1 * time.Second)

	http.HandleFunc("/api/status", statusHandler)
	http.HandleFunc("/", rootHandler)

	log.Printf("Starting Fullstack DB Rotation Backend on port :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
