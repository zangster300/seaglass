# SEAGLASS

```sh
go run ./cmd/seaglass/
```

```sh
air -build.exclude_dir "web" -misc.clean_on_exit true -build.full_bin "go run ./cmd/seaglass"
air -build.exclude_dir "web" -misc.clean_on_exit true -build.full_bin "LOG_LEVEL=debug go run ./cmd/seaglass"
```
