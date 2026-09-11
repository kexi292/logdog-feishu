# Linux 原地部署

程序、配置、游标和待发报告都保留在解压目录中，不需要创建专用用户或向 `/opt`、`/var/lib` 复制文件。使用哪个账号启动，就需要该账号对日志有读取权限、对本目录有写权限。

## 1. 解压

`uname -m` 输出 `x86_64` 使用 amd64 包，输出 `aarch64` 或 `arm64` 使用 arm64 包。以下以 amd64 为例：

```bash
sha256sum -c logdog-feishu-linux-amd64.tar.gz.sha256
tar -xzf logdog-feishu-linux-amd64.tar.gz
cd logdog-feishu-linux-amd64
./logdog-feishu -h
```

校验显示 `OK`、帮助正常输出后即可配置。服务器无需 Go 或额外 TUI 运行库，只需要可用的系统 CA 和 HTTPS 出站连接。

## 2. 配置

在解压目录内执行：

```bash
./logdog-feishu configure
```

填写 Webhook、可选签名密钥、项目、服务、日志绝对路径和匹配词。移动到 `Save configuration` 回车，确认 saved 后选择 `Quit`。Webhook 与密钥只在本机 TUI 中填写，不提交到仓库。

文件位置如下：

```text
解压目录/
  logdog-feishu
  config.yaml             TUI 生成，权限 0600
  config.yaml.state       运行时生成，保存游标及待发报告
  config.yaml.state.lock  防止本目录启动多个监听实例
  licenses/
```

保持默认状态路径即可。启动前确认当前账号能读取配置里的日志文件，也能遍历其父目录。

## 3. 前台验证

```bash
./logdog-feishu run
```

确认每个项目/服务显示 watching，files 数量符合预期。`files=0` 不表示真实日志已被监听。按 Ctrl+C 退出会保存状态；关掉 SSH 前应切换到后台运行方式。

程序启动后会向配置的群发送实际命中的告警。需要验证发送链路时，选择独立测试日志并确认允许发送测试告警，再追加关键字和堆栈；不要向生产日志注入测试内容。

## 4. systemd 常驻与开机自启

应用文件仍在本目录，systemd 只注册指向此处的服务链接，并管理系统运行日志。以下命令以 root 执行，服务也以 root 运行。先停止已有的前台或 nohup 监听，再执行；注册后不要移动或重命名解压目录。

在解压目录内执行这一段，即可生成服务文件、注册并启动：

```bash
logdog_dir="$(pwd -P)"
cat > logdog-feishu.service <<EOF
[Unit]
Description=Logdog Feishu log alerts

[Service]
WorkingDirectory=$logdog_dir
ExecStart="$logdog_dir/logdog-feishu" run
Restart=on-failure
RestartSec=15s
TimeoutStopSec=45s
UMask=0077
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

systemctl enable "$logdog_dir/logdog-feishu.service" &&
systemctl daemon-reload &&
systemctl start logdog-feishu
```

`run` 默认读取工作目录里的 `config.yaml`。失败后等待 15 秒重启；停止时最多等待 45 秒，留出保存状态和收尾发送的时间。`UMask` 与 `NoNewPrivileges` 保留文件权限和进程权限保护。开机时若网络还没就绪，失败重启机制会继续尝试。

确认状态：

```bash
systemctl status logdog-feishu --no-pager
journalctl -u logdog-feishu -n 30 --no-pager
```

若提示同名服务已存在，先用 `systemctl cat logdog-feishu` 确认已有配置，勿覆盖正在使用的其他安装实例。目录路径须不包含 `%`、反斜线、双引号或换行，这些字符在 systemd 配置中需要额外转义。

若希望完全不向系统目录注册文件，可以先前台运行；systemd 自动重启和开机自启只在完成此步骤后提供。

## 日常维护

- 修改配置：在解压目录执行 `./logdog-feishu configure`，保存后 `systemctl restart logdog-feishu`。
- 查看运行日志：`journalctl -u logdog-feishu -f`。
- 停止服务：`systemctl stop logdog-feishu`。
- 升级：停止进程，备份旧二进制，再替换本目录的 `logdog-feishu`，保留配置和状态后启动。不要把新的版本目录直接当作全新实例运行。
- 移走或删除应用目录前：先停止并禁用已注册的服务，清理自己创建的服务链接；确认不再需要恢复状态后再处理目录。

发送最终失败时进程退出，systemd 等待 15 秒后重启并优先重发待发报告。不要删除状态文件来解决网络问题。静态构建成功不代表完成真实 Linux 长期运行和飞书链路验收。

## 许可证

安装包没有为上游遗留代码补授许可证。对外分发前仍需确认上游授权；第三方组件的许可信息保留在 `licenses/`。
