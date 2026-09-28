# oci-squash

[English](README.md) | 中文

用 Go 实现、原生支持 OCI 的镜像层合并(squash)工具。把镜像顶部的若干层合并成一层,减少层数与体积。设计以**压缩耗时最小**为第一优先级(见 [`docs/技术方案.md`](docs/技术方案.md))。

## 特性

- 原生 OCI:读写 OCI layout / OCI archive / docker-archive,可远程拉取 registry 镜像
- **并行压缩**:合并层用 `klauspost` 的并行 gzip(pgzip)/ zstd,不走单线程标准库
- **单遍管道**:合并 → tee → 同时算 diff_id(未压缩)与 digest(压缩),压缩只做一遍
- **move 层零成本**:被保留的下层直接复用原压缩 blob,不解压不重压
- 完整 whiteout 语义:普通 `.wh.`、opaque `.wh..wh..opq`、跨保留层的标记重发射、marker reduction、符号/硬链接延后处理

## 构建

```
go build -o oci-squash ./cmd/oci-squash
```

## 用法

```
oci-squash [flags] <source>
```

### 来源 `<source>`

| 前缀 | 含义 | 示例 |
|---|---|---|
| `oci:PATH` | OCI layout 目录 | `oci:./myimage` |
| `oci-archive:FILE.tar` | OCI archive | `oci-archive:img.tar` |
| `docker-archive:FILE` | `docker save` 产物 | `docker-archive:img.tar` |
| (无前缀) | registry 引用,远程拉取 | `alpine:3.20` |

### 参数

| flag | 默认 | 说明 |
|---|---|---|
| `-f, --from` | 空(全部) | squash **最后 N 层**(整数);省略则合并全部层 |
| `-o, --output` | (必填) | 输出路径 |
| `--format` | `oci` | 输出格式:`oci` \| `oci-archive` \| `docker-archive` |
| `--compression` | `zstd` | 压缩算法:`zstd` \| `gzip` \| `none` |
| `--compression-level` | `0` | 压缩级别,`0` = 各算法的快默认档 |
| `-t, --tag` | 空 | 结果镜像 tag(`name:tag`) |
| `-m, --message` | 空 | squash 层的 history comment |
| `--tmp-dir` | 系统临时目录 | 压缩 blob 临时目录 |
| `-j, --parallel` | CPU 核数 | 压缩并行度 |
| `-v, --verbose` | false | 详细日志(打印各阶段耗时) |

> `--from` 语义:`--from 3` 合并最靠近顶部的 3 层为 1 层,底部其余层原样保留;省略 `--from` 合并全部层。
> 当可合并层不足 2 层时(如 `--from 1`)会报"无需 squash"并退出。

### 示例

**合并全部层,输出可被 `docker load` 的归档:**

```bash
oci-squash -o squashed.tar --format docker-archive -t myimg:squashed \
    docker-archive:myimg.tar
```

**只合并顶部 3 层,输出 OCI layout,用 gzip:**

```bash
oci-squash -f 3 -o ./out --format oci --compression gzip oci:./myimage
```

**从 registry 拉取并合并全部层(zstd):**

```bash
oci-squash -o ./alpine-squashed --format oci -t alpine:squashed alpine:3.20
```

### 完整 docker 工作流

从一个多层镜像出发,导出 → 指定层数合并 → 加载回 docker 验证:

```bash
# 1. 导出现有镜像为 docker-archive
docker save myimg:latest -o myimg.tar

# 2. 合并顶部 3 层(-v 打印合并+压缩耗时)
oci-squash -v -f 3 -o myimg-squashed.tar \
    --format docker-archive -t myimg:squashed \
    docker-archive:myimg.tar

# 3. 加载回 docker
docker load -i myimg-squashed.tar

# 4. 确认层数减少、文件系统一致
docker history myimg:squashed
docker run --rm myimg:squashed <your-check-command>
```

合并后底部保留层仍复用原压缩 blob,只有顶部合并出的那一层需要重新压缩;whiteout(被删文件)在合并过程中被正确应用。

## 架构

```
cmd/oci-squash        CLI (cobra)
internal/oci          来源加载(归一为 v1.Image)+ 写出 OCI/docker-archive
internal/squash       选层 + newest-first 合并 + whiteout + config 重算
internal/compress     并行压缩 + 单遍双 hash 的 v1.Layer 实现
```

参考实现分析与完整设计见 [`docs/技术方案.md`](docs/技术方案.md)。

## 测试

```bash
go test ./...              # 全部单测
go test -race ./...        # 并行压缩管道的竞态检测
go test -bench=. ./internal/compress/   # 压缩吞吐基准
```

覆盖范围:

- **合并算法**:last-write-wins、普通 whiteout、opaque 目录、跨保留层的标记重发射、符号链接延后
- **whiteout 辅助函数**:路径归一化、whiteout/opaque 分类、opaque 前缀覆盖判定、marker reduction 的路径层级
- **选层**:全部 / 最后 N 层切分,以及 N≤0、N>总层数、非整数、可合并层不足 2 层等守卫
- **端到端**:用 ggcr 合成的真实多层镜像跑完整 `Squash`,校验层数、文件系统、config 自洽(diff_ids 数与层数一致)
- **压缩**:gzip / zstd / none 三档的 diff_id/digest 往返与吞吐基准
