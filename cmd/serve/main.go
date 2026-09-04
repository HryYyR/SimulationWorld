package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"

	"ecosim/internal/config"
	"ecosim/internal/engine"
)

// server 持有模拟引擎，用互斥锁保护并发访问（前端可能并发请求）。
type server struct {
	mu  sync.Mutex
	cfg *config.Root
	eng *engine.Engine
}

func main() {
	var (
		addr   = flag.String("addr", ":8080", "监听地址")
		cfgDir = flag.String("cfg", "cfg", "配置目录")
		seed   = flag.Uint64("seed", 0, "随机种子（0 使用 balance.json）")
		client = flag.String("client", "client", "前端静态资源目录")
	)
	flag.Parse()

	cfg, err := config.LoadDir(*cfgDir)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	s := uint64(*seed)
	if s == 0 {
		s = uint64(cfg.Balance.World.Seed)
	}
	srv := &server{cfg: cfg, eng: engine.New(cfg, engine.Options{Seed: s})}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", srv.handleState)
	mux.HandleFunc("/api/step", srv.handleStep)
	mux.HandleFunc("/api/run", srv.handleRun)
	mux.HandleFunc("/api/reset", srv.handleReset)
	// 静态文件禁用缓存，避免改了前端代码后浏览器仍用旧版本
	fs := noCacheFileServer(http.Dir(*client))
	mux.Handle("/", fs)

	log.Printf("生态模拟可视化服务已启动: http://localhost%s (seed=%d)", *addr, s)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

// handleState 返回当前世界快照（不推进模拟）。
func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	snap := s.eng.Snapshot(200, 2000)
	s.mu.Unlock()
	writeJSON(w, snap)
}

// handleStep 推进 1 tick 并返回新快照。
func (s *server) handleStep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.advance(1, w)
}

// handleRun 推进 N tick（?n=100）并返回新快照。
func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	n := 1
	if v := r.URL.Query().Get("n"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 0 {
			http.Error(w, "invalid n", http.StatusBadRequest)
			return
		}
		n = parsed
	}
	s.advance(n, w)
}

// handleReset 用指定种子重建世界。支持 ?seed=123 指定随机种子，缺省用 balance.json 的种子。
func (s *server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	seed := uint64(s.cfg.Balance.World.Seed)
	if v := r.URL.Query().Get("seed"); v != "" {
		parsed, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			http.Error(w, "invalid seed", http.StatusBadRequest)
			return
		}
		seed = parsed
	}
	s.mu.Lock()
	s.eng = engine.New(s.cfg, engine.Options{Seed: seed})
	snap := s.eng.Snapshot(200, 2000)
	s.mu.Unlock()
	// 注意：不能复用 handleState，它会校验 GET 方法，而这里是 POST
	writeJSON(w, snap)
}

// advance 推进 ticks 个 tick，返回推进后的快照。
func (s *server) advance(ticks int, w http.ResponseWriter) {
	s.mu.Lock()
	err := s.eng.Run(ticks, nil)
	snap := s.eng.Snapshot(200, 2000)
	s.mu.Unlock()
	if err != nil {
		// 账本守恒断言失败等错误：返回错误信息，但仍附带快照供前端查看
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{
			"error":    err.Error(),
			"snapshot": snap,
		})
		return
	}
	writeJSON(w, snap)
}

// noCacheFileServer 返回禁用缓存的静态文件服务器。
func noCacheFileServer(root http.FileSystem) http.Handler {
	fs := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		fs.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "写响应失败:", err)
	}
}
