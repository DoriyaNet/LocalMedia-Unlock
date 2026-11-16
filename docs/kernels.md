# 代理内核安装与路径

LocalMedia Unlock 不内置 mihomo、sing-box 或 Xray。控制器只负责生成配置并启动已安装的内核，因此需要你自行选择来源、版本与校验方式。

## 查找顺序

控制器启动内核时，按以下优先级解析二进制：

1. 环境变量 `LOCALMEDIA_MIHOMO_BIN`、`LOCALMEDIA_SINGBOX_BIN`、`LOCALMEDIA_XRAY_BIN`。
2. 配置文件中的 `settings.kernelPaths` 对应项。
3. `PATH` 中的 `mihomo`、`sing-box`、`xray`。

环境变量优先于配置文件，适合 systemd 或 Docker 中固定路径。修改后需要重启控制器或重新启动内核。

## 安装方式

请优先使用你信任的发行包、官方 release 或自建构建，并核对发布方的校验和/签名。下面只展示目录约定，不提供未经校验的下载命令。

### 系统包管理器

如果发行版仓库提供对应包，优先使用它：

```bash
sudo apt install mihomo        # 仅当你的仓库确实提供此包
sudo dnf install sing-box      # 包名与可用性取决于发行版
```

安装后确认命令位置：

```bash
command -v mihomo
command -v sing-box
command -v xray
```

### 手动放置

将已校验的二进制放到系统目录并赋予执行权限：

```bash
sudo install -d -m 0755 /usr/local/lib/localmedia-unlock
sudo install -m 0755 ./mihomo /usr/local/lib/localmedia-unlock/mihomo
sudo install -m 0755 ./sing-box /usr/local/lib/localmedia-unlock/sing-box
sudo install -m 0755 ./xray /usr/local/lib/localmedia-unlock/xray
```

然后在 Web UI 的“系统设置 → 内核路径”中填写绝对路径，或在 systemd 环境文件中设置：

```bash
LOCALMEDIA_MIHOMO_BIN=/usr/local/lib/localmedia-unlock/mihomo
LOCALMEDIA_SINGBOX_BIN=/usr/local/lib/localmedia-unlock/sing-box
LOCALMEDIA_XRAY_BIN=/usr/local/lib/localmedia-unlock/xray
```

修改 `/etc/localmedia-unlock/localmedia-unlock.env` 后执行（该文件由安装脚本创建，权限 `0600 root:root`；其中默认注释掉覆盖项，普通用户和代理内核进程不需要读取它）：

```bash
sudo systemctl restart localmedia-unlock
```

## systemd 模式

systemd 安装默认使用 `localmedia` 用户运行控制器。内核二进制及其依赖的数据文件必须对该用户可读、可执行。若内核需要写日志或缓存，可把工作目录放在 `/var/lib/localmedia-unlock` 下，或在单元中增加对应的 `ReadWritePaths`。

不要把内核放到 `/root` 或仅 root 可访问的目录，否则 `localmedia` 用户无法执行。

## Docker 模式

镜像不包含内核。可以将宿主机上已校验的二进制以只读方式挂载到 `/opt/kernels`，例如：

```yaml
volumes:
  - ./kernels:/opt/kernels:ro
environment:
  LOCALMEDIA_MIHOMO_BIN: /opt/kernels/mihomo
  LOCALMEDIA_SINGBOX_BIN: /opt/kernels/sing-box
  LOCALMEDIA_XRAY_BIN: /opt/kernels/xray
```

二进制必须是容器架构（通常是 Linux amd64/arm64）的静态或兼容版本；Windows 的 `.exe` 不能直接运行。

若无法解决容器内网络命名空间、内核依赖或调试问题，建议使用宿主机 systemd 模式，只把本控制器容器化并不会自动获得代理能力。

## 版本兼容

不同内核版本的配置字段可能变化。控制器会先生成配置并调用内核校验命令；如果启动失败，请在日志中查看具体的配置错误，并确认内核版本与所选功能兼容。

管理面健康检查不依赖内核可用，因此内核未安装时 Web UI 和管理 API 仍可用于配置与导入节点。

