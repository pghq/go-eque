# go-red

Golang distributed job scheduling.

## Installation

go-red may be installed using the go get command:
```
go get github.com/pghq/go-red
```
## Usage

```
import "github.com/pghq/go-red"
```

To create a new queue:

```
queue, err := red.NewQueue("name-of-your-queue")
if err != nil{
    panic(err)
}
```

## Powered By
- Redis - https://redis.io