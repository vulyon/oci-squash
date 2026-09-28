# oci-squash

English | [中文](README.zh-CN.md)

A Go tool for squashing OCI/Docker image layers, with native OCI support. It merges the top layers of an image into one to reduce layer count and size. Designed with **minimizing compression time** as the top priority (see [`docs/技术方案.md`](docs/技术方案.md)).

## Features

- Native OCI: reads/writes OCI layout, OCI archive, and docker-archive; can pull registry images
- **Parallel compression**: the merged layer is compressed with `klauspost` parallel gzip (pgzip) / zstd, never the single-threaded standard library
- **Single-pass pipeline**: merge → tee → compute diff_id (uncompressed) and digest (compressed) at the same time; compress only once
- **Zero-cost move layers**: retained lower layers reuse their original compressed blobs, no decompress/recompress
- Full whiteout semantics: regular `.wh.`, opaque `.wh..wh..opq`, marker re-emission across retained layers, marker reduction, deferred symlink/hardlink handling

## Build

```
go build -o oci-squash ./cmd/oci-squash
```

## Usage

```
oci-squash [flags] <source>
```

### Source `<source>`

| Prefix | Meaning | Example |
|---|---|---|
| `oci:PATH` | OCI layout directory | `oci:./myimage` |
| `oci-archive:FILE.tar` | OCI archive | `oci-archive:img.tar` |
| `docker-archive:FILE` | `docker save` output | `docker-archive:img.tar` |
| (no prefix) | registry reference, pulled remotely | `alpine:3.20` |

### Flags

| flag | default | description |
|---|---|---|
| `-f, --from` | empty (all) | squash the **last N layers** (integer); omit to merge all layers |
| `-o, --output` | (required) | output path |
| `--format` | `oci` | output format: `oci` \| `oci-archive` \| `docker-archive` |
| `--compression` | `zstd` | compression algorithm: `zstd` \| `gzip` \| `none` |
| `--compression-level` | `0` | compression level; `0` = each algorithm's fast default |
| `-t, --tag` | empty | result image tag (`name:tag`) |
| `-m, --message` | empty | history comment for the squashed layer |
| `--tmp-dir` | system temp dir | temp directory for the compressed blob |
| `-j, --parallel` | CPU count | compression parallelism |
| `-v, --verbose` | false | verbose logging (prints per-stage timing) |

> `--from` semantics: `--from 3` merges the top 3 layers into 1, keeping the rest of the lower
> layers intact; omitting `--from` merges all layers. When fewer than 2 layers would be squashed
> (e.g. `--from 1`), it reports "nothing to squash" and exits.

### Examples

**Squash all layers, output an archive loadable by `docker load`:**

```bash
oci-squash -o squashed.tar --format docker-archive -t myimg:squashed \
    docker-archive:myimg.tar
```

**Squash only the top 3 layers, output an OCI layout, using gzip:**

```bash
oci-squash -f 3 -o ./out --format oci --compression gzip oci:./myimage
```

**Pull from a registry and squash all layers (zstd):**

```bash
oci-squash -o ./alpine-squashed --format oci -t alpine:squashed alpine:3.20
```

### Full docker workflow

Starting from a multi-layer image: export → squash a chosen number of layers → load back into docker to verify:

```bash
# 1. Export an existing image as a docker-archive
docker save myimg:latest -o myimg.tar

# 2. Squash the top 3 layers (-v prints merge+compress timing)
oci-squash -v -f 3 -o myimg-squashed.tar \
    --format docker-archive -t myimg:squashed \
    docker-archive:myimg.tar

# 3. Load back into docker
docker load -i myimg-squashed.tar

# 4. Confirm the layer count dropped and the filesystem is unchanged
docker history myimg:squashed
docker run --rm myimg:squashed <your-check-command>
```

After squashing, the retained lower layers still reuse their original compressed blobs; only the
top merged layer needs recompression. Whiteouts (deleted files) are applied correctly during the merge.

## Architecture

```
cmd/oci-squash        CLI (cobra)
internal/oci          source loading (normalized to v1.Image) + writing OCI/docker-archive
internal/squash       layer selection + newest-first merge + whiteout + config recompute
internal/compress     parallel compression + single-pass dual-hash v1.Layer implementation
```

See [`docs/技术方案.md`](docs/技术方案.md) for the reference-implementation analysis and full design.

## Testing

```bash
go test ./...              # all unit tests
go test -race ./...        # race detection for the parallel compression pipeline
go test -bench=. ./internal/compress/   # compression throughput benchmark
```

Coverage:

- **Merge algorithm**: last-write-wins, regular whiteout, opaque directory, marker re-emission across retained layers, deferred symlinks
- **Whiteout helpers**: path normalization, whiteout/opaque classification, opaque-prefix coverage, marker-reduction path hierarchy
- **Selection**: all / last-N splitting, plus guards for N≤0, N>total, non-integer, and fewer than 2 squashable layers
- **End-to-end**: a full `Squash` over a ggcr-synthesized multi-layer image, checking layer count, filesystem, and config consistency (diff_ids count == layer count)
- **Compression**: diff_id/digest round-trip and throughput benchmark for gzip / zstd / none
