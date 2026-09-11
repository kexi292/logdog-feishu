# Linux 安装与验收

适用于有 systemd 的 Linux 服务器，需要 sudo 权限。执行命令前先确认系统和 CPU：

```bash
uname -m
cat /etc/os-release
```

`x86_64` 使用 amd64 包；`aarch64` 或 `arm64` 使用 arm64 包。其他架构先停止，不要试跑不匹配的二进制。

## 1. 上传并解压

开发机使用 `bash script/build-linux.sh` 生成两个安装包和 SHA-256 文件，位于 `dist/`。选中匹配的 `.tar.gz` 和 `.sha256`，用现有 SFTP 客户端上传到服务器的用户目录，无需公开发布。

以下以 amd64 为例，ARM64 将文件名中的 `amd64` 改成 `arm64`：

```bash
sha256sum -c logdog-feishu-linux-amd64.tar.gz.sha256
tar -xzf logdog-feishu-linux-amd64.tar.gz
cd logdog-feishu-linux-amd64
file logdog-feishu
```

校验必须显示 `OK`。可执行文件应为对应架构的 Linux ELF，并静态链接；服务器不需要 Go、Node 或额外 TUI 运行库。

## 2. 安装

```bash
id logdog || sudo useradd --system --user-group --home-dir /var/lib/logdog --shell /usr/sbin/nologin logdog
sudo install -d -m 0755 /opt/logdog
sudo install -m 0755 logdog-feishu /opt/logdog/logdog-feishu
sudo cp -R licenses /opt/logdog/
sudo install -m 0644 README.md SOURCE.txt /opt/logdog/
sudo install -d -o logdog -g logdog -m 0700 /var/lib/logdog
sudo install -m 0644 logdog-feishu.service /etc/systemd/system/logdog-feishu.service
sudo systemctl daemon-reload
```

`/opt/logdog` 存放程序，`/var/lib/logdog` 存放配置、游标和待发报告。不要删除状态文件，它负责续读和恢复发送。

## 3. 确认日志读取权限

用实际日志路径替换下面示例：

```bash
sudo -u logdog test -r /var/log/your-service/app.log && echo '日志可读'
```

目录需要遍历权限，文件需要读取权限。若不可读，先查看 `ls -ld` 和 `ls -l` 的属组，按服务原有权限方案给 logdog 添加必要的日志读取组，或者设置目录 ACL；不要把整个日志目录改成所有人可写。轮转新建的文件也必须继承读取权限。

## 4. 用 TUI 配置

```bash
sudo -u logdog /opt/logdog/logdog-feishu configure -c /var/lib/logdog/config.yaml
```

填写一个飞书群自定义机器人 Webhook、可选签名密钥、项目、服务、绝对日志路径和匹配词。选择 `Save configuration` 回车，确认显示 saved 后再选择 `Quit`。所有项目与服务共用这一个 Webhook；TUI 退出时不会发送测试消息。

无需设置状态路径，默认会保存到 `/var/lib/logdog/config.yaml.state`。Webhooks 和密钥只在服务器 TUI 中填写，不要贴到聊天或仓库。服务器需要能向飞书发出 HTTPS 请求，系统 CA 缺失时安装发行版的 `ca-certificates`。

## 5. 启动并观察

```bash
sudo systemctl enable --now logdog-feishu
sudo systemctl status logdog-feishu --no-pager
sudo journalctl -u logdog-feishu -n 50 --no-pager
```

确认 `active (running)`，并且每个配置的项目/服务都显示 watching，files 数量符合预期。`files=0` 可能是路径暂不存在或没有匹配文件，不代表真实日志已被监听。

启动成功不等于飞书链路已验收。下一步由用户选择一个独立测试日志并确认允许发测试告警，再追加匹配行，核对群里的来源、堆栈与分段；不要向生产日志注入测试内容。随后分别验证重启、轮转和网络恢复。

## 日常操作

```bash
sudo systemctl restart logdog-feishu
sudo systemctl stop logdog-feishu
sudo journalctl -u logdog-feishu -f
```

修改配置后重启生效。发送最终失败时进程退出，systemd 等待 15 秒后重启，优先重发已保存报告；错误持续时需要查看 journal，不要通过删除状态文件解决。配置格式或权限错误也会导致重启，需要修正原因后重新启动。

升级前停止服务，备份旧二进制；安装新二进制后启动服务。保留配置和状态。真实 Linux 长期运行及内存基线仍需实测，不能用静态构建成功代替。

## 许可证

本安装包用于项目本地部署验收，没有为上游遗留代码补授许可证。整个项目对外分发前仍需确认上游授权；第三方组件的许可证随包保存在 `licenses/`。
