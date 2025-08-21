#!/bin/bash

if [ $# -lt 1 ]; then
	echo "$0 <db_path>"
	echo "例如: $0 /path/to/your/erigon/data"
	exit 1
fi

SCRIPDIR=$(pwd)
cd ../..
BASEDIR=$(pwd)
cd $SCRIPDIR

echo "运行表列表工具..."
echo "数据库路径: $1"

# 运行主程序
go run main.go list-tables "$1"
