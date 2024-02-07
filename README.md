# modeld

`ollama cli` migrates to `docker cli` for model management.

```bash
~ ollama --help
Large language model runner

Usage:
  ollama [flags]
  ollama [command]

Available Commands:
  serve       Start ollama                    - *out of scope
  create      Create a model from a Modelfile - *out of scope
  show        Show information for a model    - docker image inspect - not implemented
  run         Run a model                     - docker run           - not implemented
  pull        Pull a model from a registry    - docker pull          - ok/progress report wip
  push        Push a model to a registry      - docker push          - not implemented
  list        List models                     - docker images        - ok
  cp          Copy a model                    - docker image tag     - not implemented
  rm          Remove a model                  - docker rmi           - ok/untested
```

## How to use

modeld:

```bash
cd cmd/modeld
go run main.go
```

docker cli:

```bash
cd cmd/modeld
docker -H unix://$PWD/model.sock images
```