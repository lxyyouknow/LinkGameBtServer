// check-config 仅检查环境变量，不建立数据库或平台连接。
package main

import (
	"fmt"
	"linkgame-server/internal/config"
	"os"
)

func main() {
	if err := config.ValidateBTEnvironment(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if _, err := config.Load(); err != nil {
		fmt.Fprintln(os.Stderr, "BT 运行配置未就绪：请检查 app.env 必填字段、GM 配置、CORS 与平台参数")
		os.Exit(2)
	}
	fmt.Println("BT 运行环境检查通过；未连接数据库或平台")
}
