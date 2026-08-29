#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
fixture_root="$repository_root/tests/fixtures/go-consumers"

run_go_public() {
	env GOWORK=off GOPRIVATE= GONOPROXY= GONOSUMDB= \
		GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org "$@"
}

copy_fixture() {
	component=$1
	destination=$2
	cp "$fixture_root/$component/main.go" "$destination/main.go"
}

initialize_consumer() {
	directory=$1
	module_name=$2
	mkdir -p "$directory"
	(
		cd "$directory"
		go mod init "$module_name" >/dev/null
		go mod edit -go=1.24
	)
}

run_local_consumer() {
	component=$1
	temporary_root=$2
	consumer_dir="$temporary_root/$component-consumer"
	initialize_consumer "$consumer_dir" "example.com/stellarmesh-$component-consumer"
	copy_fixture "$component" "$consumer_dir"
	(
		cd "$consumer_dir"
		case "$component" in
			gateway)
				go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway@v0.0.0
				go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway=../gateway-sdk
				go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging=../logging-sdk
				;;
			logging)
				go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.0.0
				go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging=../logging-sdk
				;;
			parent)
				go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.0.0
				go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go=../sdk-go
				;;
			kafka)
				go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.0.0
				go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka=../kafka-sdk
				;;
			combined)
				go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.0.0
				go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway@v0.0.0
				go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.0.0
				go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.0.0
				go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go=../sdk-go
				go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway=../gateway-sdk
				go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging=../logging-sdk
				go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka=../kafka-sdk
				;;
			*)
				echo "不允许的本地 Go Module 组件: $component" >&2
				exit 1
				;;
		esac
		GOWORK=off go mod tidy
		GOWORK=off go run .
		if [ "$component" = "logging" ]; then
			module_graph=$(GOWORK=off go list -m all)
			if printf '%s\n' "$module_graph" | grep -Eq \
				'^github\.com/L1ndenbaum/stellarmesh-sdk/sdk/go v|sdk/go/mq/kafka|aws-sdk-go-v2|go-chi/chi|golang-jwt|redis/go-redis|segmentio/kafka-go'; then
				echo "Logging-only 消费者引入了父 SDK 或非 Logging 依赖" >&2
				exit 1
			fi
		fi
	)
}

run_local() {
	temporary_root=$(mktemp -d)
	trap 'rm -rf "$temporary_root"' EXIT INT TERM

	cp -R "$repository_root/sdk/go" "$temporary_root/sdk-go"
	mv "$temporary_root/sdk-go/gateway" "$temporary_root/gateway-sdk"
	mv "$temporary_root/sdk-go/logging" "$temporary_root/logging-sdk"
	mv "$temporary_root/sdk-go/mq/kafka" "$temporary_root/kafka-sdk"

	(
		cd "$temporary_root/gateway-sdk"
		GOWORK=off go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging=../logging-sdk
		GOWORK=off go test ./...
		module_graph=$(GOWORK=off go list -m all)
		if printf '%s\n' "$module_graph" | grep -Eq \
			'^github\.com/L1ndenbaum/stellarmesh-sdk/sdk/go v|sdk/go/mq/kafka|aws-sdk-go-v2|go-chi/chi|segmentio/kafka-go'; then
			echo "Gateway-only 消费者引入了父 SDK、对象存储、Chi 或 Kafka 依赖" >&2
			exit 1
		fi
	)
	(
		cd "$temporary_root/logging-sdk"
		GOWORK=off go test ./...
		module_graph=$(GOWORK=off go list -m all)
		if [ "$(printf '%s\n' "$module_graph" | wc -l | tr -d ' ')" -ne 1 ]; then
			echo "Logging Module 不应包含第三方依赖" >&2
			exit 1
		fi
	)
	(
		cd "$temporary_root/kafka-sdk"
		GOWORK=off go test ./...
	)
	(
		cd "$temporary_root/sdk-go"
		GOWORK=off go test ./...
		if GOWORK=off go list -m all | grep -q '^github.com/segmentio/kafka-go '; then
			echo "父 SDK 不应包含 kafka-go" >&2
			exit 1
		fi
	)

	run_local_consumer gateway "$temporary_root"
	run_local_consumer logging "$temporary_root"
	run_local_consumer parent "$temporary_root"
	run_local_consumer kafka "$temporary_root"
	run_local_consumer combined "$temporary_root"
}

resolve_public_component() {
	tag=$1
	case "$tag" in
		sdk/go/gateway/v*.*.*)
			component=gateway
			module_path=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway
			module_version=${tag#sdk/go/gateway/}
			;;
		sdk/go/logging/v*.*.*)
			component=logging
			module_path=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging
			module_version=${tag#sdk/go/logging/}
			;;
		sdk/go/mq/kafka/v*.*.*)
			component=kafka
			module_path=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka
			module_version=${tag#sdk/go/mq/kafka/}
			;;
		sdk/go/v*.*.*)
			component=parent
			module_path=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go
			module_version=${tag#sdk/go/}
			;;
		*)
			echo "不允许的 Go Module tag: $tag" >&2
			exit 1
			;;
	esac
}

run_public() {
	tag=${1:-}
	if [ -z "$tag" ]; then
		echo "public 模式需要传入 Go Module tag" >&2
		exit 1
	fi
	resolve_public_component "$tag"

	attempt=1
	while [ "$attempt" -le 20 ]; do
		if run_go_public go mod download "$module_path@$module_version"; then
			break
		fi
		if [ "$attempt" -eq 20 ]; then
			echo "公共 Go 代理未在限定时间内收录 $module_path@$module_version" >&2
			exit 1
		fi
		sleep 15
		attempt=$((attempt + 1))
	done

	temporary_root=$(mktemp -d)
	trap 'rm -rf "$temporary_root"' EXIT INT TERM
	consumer_dir="$temporary_root/public-consumer"
	initialize_consumer "$consumer_dir" "example.com/stellarmesh-public-consumer"
	copy_fixture "$component" "$consumer_dir"
	(
		cd "$consumer_dir"
		go mod edit -require="$module_path@$module_version"
		run_go_public go mod tidy
		run_go_public go run .
	)
}

mode=${1:-local}
case "$mode" in
	local)
		run_local
		;;
	public)
		shift
		run_public "${1:-}"
		;;
	*)
		echo "用法: $0 [local | public <module-tag>]" >&2
		exit 1
		;;
esac
