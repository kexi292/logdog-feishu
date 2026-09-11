# LogAlert

重量只有1克的轻量级日志监控告警程序

## 功能

* 日志文件配置支持glob
* 监控关键字配置
* 告警CURL配置

## 运行

首次使用直接启动配置界面，不需要记忆 YAML 写法：

```bash
go run . configure -c config.yaml
```

在界面中填写 Webhook、项目、服务、日志路径和关键字，按 `Ctrl+S` 保存。后台监听使用：

```bash
go run . run -c config.yaml
```

## 配置文件

配置文件由 TUI 生成，也可以由后台命令读取。Webhook 和签名密钥只保存在本地配置文件中。

```yaml
inputs:
    # 项目名称，没啥用
  - name: project-000
    # paths扫描频率(秒)
    scan_frequency: 10
    # 监控的文件，支持glob
    paths:
      - /data/log/*.log
      - /data/log1/log1.log
    # 监控内容，包含内容即报警
    include_lines: ['error', 'warning']
    # 排除监控内容，包含不告警
    exclude_lines:
      - "success"
      - "warning"
    # 另一个项目配置
  - name: project-001
    paths:
      - /var/log/*.log
    include_lines: ['success']
output.http:
  method: POST
  # 这里是企微机器人的地址
  url: https://open.feishu.cn/open-apis/bot/v2/hook/REPLACE_ME
  # Header头
  headers:
    - Content-Type application/json;charset=UTF-8
  # 留着扩展用
  format: json
  # 请求内容(%{content}会替换为日志告警行的内容)
  body: >
    {
      "msgtype": "markdown",
      "markdown": {
        "content": "DIY报警内容\n<font color=\"warning\">%{content}</font>"
      }
    }
```
