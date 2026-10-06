// Package dbtest 为数据库测试提供真实的 PostgreSQL。每个测试包在 TestMain 里起一个 postgres:18 容器，
// 执行一次迁移作为模板库；每个测试从模板复制出自己的库，互不干扰，可以并行：
//
//	func TestMain(m *testing.M) { dbtest.Main(m) }
//
//	func TestXxx(t *testing.T) {
//		t.Parallel()
//		pool := dbtest.Pool(t)
//		store := repository.NewStore(pool)
//		...
//	}
//
// 要自己创建连接池时（例如在 testing/synctest 的气泡里）用 dbtest.Config(t) 取得新库的连接配置。
//
// 需要 Docker。go test -short 跳过数据库测试；不加 -short 而 Docker 不可用时直接失败。
package dbtest

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database"
)

const (
	image        = "postgres:18"
	templateName = "danfuse" // 容器启动时建好的库，执行迁移后作为模板
)

// 由 Main 设置，测试运行期间只读。
var (
	admin *pgxpool.Pool   // 连到维护库 postgres，用来建库、删库
	base  *pgxpool.Config // 各测试库的连接配置，复制后换掉库名
	seq   atomic.Int64
)

// Main 在 TestMain 中调用：起容器并执行迁移得到模板库，运行测试，最后销毁容器并以测试结果退出。
// -short 时不起容器，直接运行测试（用到 Pool 的测试会跳过）。
func Main(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	ctr, err := postgres.Run(ctx, image,
		postgres.WithDatabase(templateName),
		// 追加在模块默认的 -c fsync=off 之后：并行的测试各有连接池，默认的 100 个连接不够用
		testcontainers.WithCmdArgs("-c", "max_connections=500"),
		postgres.BasicWaitStrategies(),
	)
	defer func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			fmt.Fprintln(os.Stderr, "dbtest:", err)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest: 数据库测试需要 Docker，启动 %s 容器失败：%v\n没有 Docker 时用 go test -short 跳过数据库测试。\n", image, err)
		return 1
	}

	if err := setup(ctx, ctr); err != nil {
		fmt.Fprintln(os.Stderr, "dbtest:", err)
		return 1
	}
	defer admin.Close()

	return m.Run()
}

// setup 在容器的初始库上执行迁移，作为模板库；再连上维护库。
func setup(ctx context.Context, ctr *postgres.PostgresContainer) error {
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return fmt.Errorf("get connection string: %w", err)
	}

	// 与服务启动走同一条路径执行迁移。完成后立即关闭连接：模板库上有连接时不能复制
	template, err := database.Connect(ctx, config.Database{DSN: dsn}, slog.New(slog.DiscardHandler))
	if err != nil {
		return fmt.Errorf("connect template database: %w", err)
	}
	err = database.Migrate(ctx, template, slog.New(slog.DiscardHandler))
	template.Close()
	if err != nil {
		return fmt.Errorf("migrate template database: %w", err)
	}

	base, err = pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse connection string: %w", err)
	}
	cfg := base.Copy()
	cfg.ConnConfig.Database = "postgres"
	admin, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect maintenance database: %w", err)
	}
	return nil
}

// Config 从模板复制出一个新库，返回连到它的连接配置；测试结束时删掉这个库（连接池要在那之前关闭）。
// 需要自己控制连接池在哪里创建、关闭时用它，例如 testing/synctest 的气泡里。-short 时跳过当前测试。
func Config(t testing.TB) *pgxpool.Config {
	t.Helper()
	if testing.Short() {
		t.Skip("数据库测试，-short 时跳过")
	}
	if admin == nil {
		t.Fatal("dbtest: 需要先在 TestMain 中调用 dbtest.Main")
	}

	name := fmt.Sprintf("test_%d", seq.Add(1))
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+name+" TEMPLATE "+templateName); err != nil {
		t.Fatalf("dbtest: create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		// t.Context() 在 Cleanup 之前已被取消
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dbtest: drop database %s: %v", name, err)
		}
	})

	cfg := base.Copy()
	cfg.ConnConfig.Database = name
	return cfg
}

// Pool 从模板复制出一个新库，返回连到它的连接池；测试结束时关闭连接池并删掉这个库。
// -short 时跳过当前测试。
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	cfg := Config(t)
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("dbtest: connect database %s: %v", cfg.ConnConfig.Database, err)
	}
	t.Cleanup(pool.Close)
	return pool
}
