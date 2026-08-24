# 文物预防性养护巡检闭环服务

这是一个面向博物馆和档案馆的 HTTP JSON API，覆盖文物档案、排期防冲突、分材质风险评估、逐项巡检及更正、逐异常处置、结果复核和可验证审计关闭的完整闭环。服务使用本地 JSON 快照与追加事件日志保存数据，任务更新通过 `revision` 乐观并发控制，写入支持带请求指纹的 `Idempotency-Key`。

## 构建、运行与测试

```bash
go test ./...
go run ./cmd/heritage-care -addr=127.0.0.1:19081
```

启动地址可通过 `-addr=127.0.0.1:<port>` 指定，也可设置 `PORT`（仅端口号，绑定到 `127.0.0.1`）。使用 `-self-check` 执行有界自检并退出：

```bash
go run ./cmd/heritage-care -addr=127.0.0.1:19082 -self-check
```

## 闭环接口

- `POST /v1/conservation-tasks` 校验 RFC3339 计划窗口、未关闭任务交叠和幂等指纹，并固化风险规则版本、评分明细、适用阈值与检查清单。
- `POST /v1/inspections/{task_id}` 通过 `checklist_results` 接收逐项结论、规范单位的测量值和证据；携带 `supersedes_inspection_id`、`correction_reason` 与 `revision` 可在处置前追加更正版本。
- `POST /v1/actions/{task_id}` 按 `anomaly_code` 创建单项建议；`POST /v1/actions/{recommendation_id}` 由责任人提交结果，`PATCH /v1/actions/{recommendation_id}/review` 执行批准或驳回。
- `GET /v1/conservation-tasks/{task_id}` 返回任务及闭环进度；列表入口支持 `owner_id`、`status`、`overdue`、`artifact_id`、`limit` 和 `cursor`。
- `PATCH /v1/conservation-tasks/{task_id}/close` 原子写入关闭状态与最终摘要，`GET /v1/audits/{task_id}` 返回并校验结构化哈希链时间线。

所有创建类请求继续使用 `Idempotency-Key`。涉及状态变化的更正、结果提交、复核和关闭请求应携带服务端最新 `revision`；处置结果与复核还可通过 `action_revision` 检测同一建议的并发更新。
