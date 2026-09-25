#!/bin/sh
set -eu

# 仅初始化本轮测试容器；凭据和 CLI 配置随调用方的临时目录一起清理。
directory=$1
endpoint=$2
bucket=$3
admin_user=$4
admin_password=$5
project_user=$6
project_password=$7
case "$(uname -s)/$(uname -m)" in
    Linux/x86_64)
        architecture=amd64
        checksum=4a8128911ccad4e7b481f26635a4cfd1ec064412210526e57ad2c748d356f3b7
        ;;
    Linux/aarch64|Linux/arm64)
        architecture=arm64
        checksum=6e9dea7ac4f81562add9e1e31206ba6059d6f5929ad4ed924986223282b3aa9c
        ;;
    *) echo 'RustFS 集成入口要求 Linux amd64 或 arm64' >&2; exit 1 ;;
esac
curl -fsSL --retry 3 \
    "https://github.com/rustfs/cli/releases/download/v0.1.36/rustfs-cli-linux-$architecture-v0.1.36.tar.gz" \
    -o "$directory/rc.tar.gz"
printf '%s  %s\n' "$checksum" "$directory/rc.tar.gz" | sha256sum -c - >/dev/null
tar -xzf "$directory/rc.tar.gz" -C "$directory"
export RC_CONFIG_DIR="$directory/rc-config"
rc="$directory/rc"
"$rc" alias set admin "$endpoint" "$admin_user" "$admin_password" --bucket-lookup path >/dev/null
"$rc" bucket create "admin/$bucket" >/dev/null
"$rc" bucket version enable "admin/$bucket" >/dev/null
"$rc" admin user add admin "$project_user" "$project_password" >/dev/null
"$rc" admin policy create admin storage-project "$directory/project-policy.json" >/dev/null
"$rc" admin policy attach admin storage-project --user "$project_user" >/dev/null
"$rc" alias set project "$endpoint" "$project_user" "$project_password" --bucket-lookup path >/dev/null
if "$rc" bucket create project/forbidden-bucket >"$directory/denied.log" 2>&1; then
    echo '项目凭据不应拥有 Bucket 创建权限' >&2
    exit 1
fi
# 网络或 CLI 故障不能冒充权限拒绝。
grep -q 'AccessDenied' "$directory/denied.log" || {
    cat "$directory/denied.log" >&2
    exit 1
}
