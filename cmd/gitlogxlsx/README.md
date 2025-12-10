# gitlogxlsx

## TL;TR

go run . --author "github用户名"

## 简介

从指定 Git 仓库读取提交记录，按 `--author` 过滤，导出为符合「tRPC_Go需求导入模板」列顺序的 XLSX：标题、需求类别、处理人、预估工时、预计开始、预计结束、详细描述。

## 用法
```bash
go run ./cmd/gitlogxlsx \
  --author "Alice" \
  --repo /path/to/repo \
  --output /tmp/commits.xlsx \
  --category MF \
  --estimate 0.5 \
  --since "2024-10-01" \
  --until "2024-10-31" \
  --date-format "2006-01-02"
```

### 主要参数
- `--author`：必填，Git 提交者匹配字符串（git log 的 --author 语法）。
- `--repo`：仓库路径，默认当前目录。
- `--output`：导出文件路径，默认 `git_commits.xlsx`，不存在的目录会自动创建。
- `--category`：需求类别列值，默认 `MF`。
- `--estimate`：预估工时列值，默认 `0.5`。
- `--since` / `--until`：透传给 `git log` 的时间范围（可选）。
- `--date-format`：日期格式（预计开始/结束），默认 `2006-01-02`（示例 `2025-12-01`）。

## 输出格式
- 工作表名：`tRPC_Go需求导入模板`。
- 列宽：A=50，B-D=20，E-F=20，G=100。
- 值映射：
  - 标题：提交标题（空则回退 hash）。
  - 需求类别：`--category` 指定值。
  - 处理人：提交作者名。
  - 预估工时：`--estimate` 值。
  - 预计开始/预计结束：提交时间按 `--date-format` 格式化。
  - 详细描述：提交标题与正文，换行拼接。
