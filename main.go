package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"monitorDNS/internal/monitor"
	"monitorDNS/internal/store"
	"monitorDNS/internal/web"
)

var (
	// legacy / single-target mode
	domain     string
	recordType string
	interval   int
	console    bool

	// web mode
	webMode bool
	addr    string
	dbPath  string
)

func main() {
	flag.BoolVar(&webMode, "web", false, "是否启动 Web 模式（SQLite + 登录 + 页面管理）")
	flag.StringVar(&addr, "addr", "127.0.0.1:8080", "Web 监听地址")
	flag.StringVar(&dbPath, "db", "monitorDNS.db", "SQLite 数据库文件路径")

	flag.StringVar(&domain, "d", "", "要解析的域名（非 Web 模式）")
	flag.StringVar(&recordType, "t", "A", "记录类型：A 或 CNAME（非 Web 模式）")
	flag.IntVar(&interval, "i", 10, "检测间隔（秒）（非 Web 模式）")
	flag.BoolVar(&console, "p", false, "是否打印到控制台（非 Web 模式）")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if webMode {
		if err := runWeb(ctx); err != nil {
			log.Fatalf("web 模式启动失败: %v", err)
		}
		return
	}

	if domain == "" {
		flag.Usage()
		os.Exit(1)
	}
	runLegacy(ctx)
}

func runWeb(ctx context.Context) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	user, pass, created, err := st.EnsureAutoAdminUser(ctx)
	if err != nil {
		return err
	}
	if created {
		_ = os.WriteFile("admin_credentials.txt", []byte(fmt.Sprintf("username: %s\npassword: %s\n", user, pass)), 0600)
		log.Printf("已自动创建账号：%s / %s（同时写入 admin_credentials.txt）", user, pass)
	}

	mon := monitor.NewManager(st)
	go mon.Start(ctx)

	srv := web.NewServer(st)
	log.Printf("Web 已启动：http://%s", addr)
	return srv.ListenAndServe(ctx, addr)
}

func runLegacy(ctx context.Context) {
	intervalDur := time.Duration(interval) * time.Second
	if intervalDur <= 0 {
		intervalDur = 10 * time.Second
	}

	file, err := os.Create("log.txt")
	if err != nil {
		fmt.Println("没有权限创建日志文件")
		os.Exit(1)
	}
	defer file.Close()

	log.SetOutput(file)
	start := time.Now()

	var last string
	changeCount := 0
	maxInt, minInt, sumInt := 0, 0, 0
	changeInterval := 0

	t := time.NewTicker(intervalDur)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			value, errStr := monitor.Lookup(domain, recordType)
			now := time.Now().Format("2006-01-02 15:04:05")

			if errStr != nil {
				log.Printf("解析错误: %v", *errStr)
				if console {
					fmt.Printf("%s %s 解析错误: %v\n", now, domain, *errStr)
				}
				changeInterval += interval
				continue
			}

			if last == "" {
				last = value
				log.Printf("%s(%s) 的解析是: %v", domain, recordType, value)
				if console {
					fmt.Printf("%s %s(%s) 的解析是: %v\n", now, domain, recordType, value)
				}
				changeInterval = 0
				continue
			}

			if last != value {
				changeCount++
				if minInt == 0 || changeInterval < minInt {
					minInt = changeInterval
				}
				if changeInterval > maxInt {
					maxInt = changeInterval
				}
				sumInt += changeInterval
				avg := sumInt / changeCount

				log.Printf("%s(%s) 解析发生变化: %v -> %v, 间隔 %d 秒", domain, recordType, last, value, changeInterval)
				log.Printf("已监控 %d 秒, 变化 %d 次, 最长 %d 秒, 最短 %d 秒, 平均 %d 秒",
					int(time.Since(start).Seconds()), changeCount, maxInt, minInt, avg)
				if console {
					fmt.Printf("%s %s(%s) 解析发生变化: %v -> %v, 间隔 %d 秒\n", now, domain, recordType, last, value, changeInterval)
					fmt.Printf("%s 已监控 %d 秒, 变化 %d 次, 最长 %d 秒, 最短 %d 秒, 平均 %d 秒\n",
						now, int(time.Since(start).Seconds()), changeCount, maxInt, minInt, avg)
				}
				last = value
				changeInterval = 0
			} else {
				log.Printf("%s(%s) 的解析是: %v", domain, recordType, value)
				if console {
					fmt.Printf("%s %s(%s) 的解析是: %v\n", now, domain, recordType, value)
				}
				changeInterval += interval
			}
		}
	}
}
