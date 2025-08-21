#!/bin/bash

if [ $# -lt 1 ]; then
	echo "$0 <db_path> [level]"
	echo "Levels: conservative (default), moderate, aggressive"
	echo "例如: $0 /path/to/your/erigon/data"
	echo "例如: $0 /path/to/your/erigon/data aggressive"
	exit 1
fi

SCRIPDIR=$(pwd)
cd ../..
BASEDIR=$(pwd)
cd $SCRIPDIR

echo "运行 chaindata 裁剪工具..."
echo "数据库路径: $1"

if [ $# -eq 2 ]; then
	echo "裁剪级别: $2"
	# 运行主程序
	go run main.go prune-chaindata "$1" "$2"
else
	echo "裁剪级别: conservative (默认)"
	# 运行主程序
	go run main.go prune-chaindata "$1"
fi
