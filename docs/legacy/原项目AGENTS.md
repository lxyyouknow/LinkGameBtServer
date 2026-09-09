# LinkGame 服务端协作约定

- 所有说明和运维文档默认使用中文。
- 技术栈固定为 Go 单体服务与 MySQL 8；正式运行使用 Linux amd64、用户级 systemd。
- 商业化计划已接入：包含 TikTok 激励/插屏、一次性奖励会话、每日广告礼包、广告事件与 GM 广告报表；新增业务位仍必须以产品计划书为准，不得自行猜测奖励或统计口径。
- 登录支持游客、免密码测试账号和关闭状态的 TikTok 预留。平台 OpenID 只能由服务端用一次性 code 换取。
- 数据库只保存会话 Token 的 SHA-256，不保存明文 Token、平台 code 或平台 access token。
- 云存档使用 revision 乐观锁；关卡与教程只前进不后退；金币、道具和主题碎片必须使用幂等 mutation。
- 主题 0 默认完整激活；普通主题 27 片、限定主题 34 片；主题编号固定为 0～58。
- 道具固定为 `hint`、`shuffle`、`remove`。经济资产不能用 `max(local, cloud)` 合并。
- 迁移文件一旦进入环境就禁止修改，只能新增向前 migration；正式库不自动执行 down migration。
- `.env`、`.release.env`、SSH 私钥、数据库密码、AWS AK/SK、GM 密码不得提交或写入日志。
- 默认不提交、不推送、不部署、不迁移正式数据库，除非 lxy 在当前请求中明确授权。
- 修改 Go 代码后执行 `gofmt`、`go test ./...`、`go vet ./...`。
