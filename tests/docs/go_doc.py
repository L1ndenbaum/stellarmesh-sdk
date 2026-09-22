"""使用标准 go doc 检查关键配置说明仍可查阅，不生成文档站。"""

import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def main() -> None:
    for symbol, expected in (
        ("./sdk/go/http/jsonbody.Options", "1 MiB"),
        ("./sdk/go/gateway.HealthConfig", "2 秒"),
        ("./sdk/go/gateway/sessionauth.StoreConfig", "Sentinel"),
        ("./sdk/go/logging.HandlerOptions", "16384"),
        ("./sdk/go/mq/kafka.ConnectionConfig", "10 秒"),
        ("./sdk/go/objectstorage/s3store.Config", "15 分钟"),
    ):
        output = subprocess.check_output(["go", "doc", symbol], cwd=ROOT, text=True)
        if expected not in output:
            raise ValueError(f"{symbol} 缺少配置说明 {expected}")
    print("Go 公共配置的 go doc 查阅检查通过")


if __name__ == "__main__":
    main()
