# BENZHI_README

## 项目说明
- 项目：benzhi-project-98a5a834-abe8-4c2b-ad5b-9ab05a11ec3d
- 项目用途：提供文物预防性养护任务从创建、风险评估、现场巡检、处置分派、结果复核到审计关闭的可追踪 HTTP JSON API 服务。
- Go 工具链：`golang:1.22`
- 前端工具链：无

## 项目描述
- 项目名称：文物预防性养护巡检闭环服务
- 项目概述：为博物馆和档案馆提供文物预防性养护任务的创建、风险评估、现场巡检、处置复核与审计关闭的一体化 HTTP API，仅实现一条可追踪的养护闭环流程。
- 核心工作流：养护任务创建→风险评分与清单生成→现场巡检记录→处置建议分派→结果复核→审计关闭
- 对外接口：HTTP JSON API：提供文物档案、养护任务、巡检记录和处置复核端点；服务支持 -addr=127.0.0.1:<port> 或 PORT 环境变量，默认监听 127.0.0.1:19081，提供自检入口。

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...
cd /app && GOTOOLCHAIN=local go run ./cmd/heritage-care -addr=127.0.0.1:19082 -self-check
cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-project-98a5a834-abe8-4c2b-ad5b-9ab05a11ec3d-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-project-98a5a834-abe8-4c2b-ad5b-9ab05a11ec3d-arm64 linux/arm64
docker run -it benzhi-project-98a5a834-abe8-4c2b-ad5b-9ab05a11ec3d-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/heritage-care -addr=127.0.0.1:19082 -self-check`
