# 数据库表列表工具

这个工具可以帮助你查看 Erigon 数据库中的实际表结构，特别是 SMT 数据库分库前后的表分布情况。

## 工具说明

### 1. 表列表工具 (`list-tables.go`)

这个工具可以：
- 列出 Chaindata 数据库中的所有表
- 对表进行分类分析
- 检查 SMT 数据库是否存在
- 显示 SMT 相关表的定义

### 2. 运行脚本 (`list-tables.sh`)

提供了一个便捷的脚本来运行表列表工具。

## 使用方法

### 方法 1: 使用运行脚本

```bash
cd cmd/prune-smt-data
./list-tables.sh /path/to/your/erigon/data
```

### 方法 2: 直接运行 Go 程序

```bash
cd cmd/prune-smt-data
go run list-tables.go /path/to/your/erigon/data
```

## 输出说明

工具会输出以下信息：

1. **数据库路径信息**
   - 检查的数据库路径
   - Chaindata 和 SMT 数据库路径

2. **数据库信息**
   - 页面大小
   - 映射大小
   - 标志位

3. **表列表**
   - 按字母顺序排列的所有表名
   - 表的总数量

4. **表分类分析**
   - SMT 相关表
   - 基础区块表
   - 状态数据表
   - 历史数据表
   - 索引表
   - Trie 表
   - ZKEVM 表
   - Beacon 表
   - 系统表
   - 未分类的表

5. **总结**
   - 总表数
   - SMT 相关表定义

## 示例输出

```
检查数据库路径: /path/to/erigon/data
Chaindata 路径: /path/to/erigon/data/chaindata
SMT 路径: /path/to/erigon/data/smt

=== Chaindata 数据库中的表 (共 120 个) ===
  1. AccountChangeSet
  2. AccountHistory
  3. BadHeaderNumber
  4. BeaconBlock
  5. BeaconState
  ...

=== 表分类分析 ===

SMT 相关表 (找到 5/5):
  - HermezSmt
  - HermezSmtAccountValues
  - HermezSmtHashKey
  - HermezSmtMetadata
  - HermezSmtStats

基础区块表 (找到 12/12):
  - BadHeaderNumber
  - BlockBody
  - EthTx
  - HeaderCanonical
  - HeaderNumber
  - HeaderTD
  - Headers
  - Log
  - NonCanonicalTxs
  - Receipts
  - Senders
  - TxLookup

...

=== 总结 ===
Chaindata 数据库中共有 120 个表
SMT 相关表定义: [HermezSmt HermezSmtStats HermezSmtAccountValues HermezSmtMetadata HermezSmtHashKey]
```

## 注意事项

1. 确保数据库路径正确，应该指向包含 `chaindata` 和 `smt` 子目录的 Erigon 数据目录
2. 工具需要读取权限来访问数据库文件
3. 如果 SMT 数据库不存在，工具会提示但不会报错
4. 表分类是基于预定义的分类规则，可能不是完全准确的

## 故障排除

如果遇到问题：

1. **数据库路径不存在**
   - 检查路径是否正确
   - 确保路径指向 Erigon 数据目录

2. **权限错误**
   - 确保有读取数据库文件的权限

3. **编译错误**
   - 确保在正确的目录下运行
   - 检查 Go 环境是否正确配置
