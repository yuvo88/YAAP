# YAAP - Yet Another Ai Program

## Goal
This is my personal take on local LLMs, it is meant to create a simple terminal experience for interacting with Local LLMs.
This project is not meant to be a coding agent, it is not meant to automate every part of your life.
It is meant to create a private, personal and usable ChatGPT like experience with a local model.

## Features
This is a work in progress and is meant for personal use, and as such I will not promise any features.

### Modes
A local LLM chat decides whether to search the web or how hard to think, I think this is suboptimal.
The user knows what it wants the bot to do the best, modes let you control what and how much of it your model does.
There are several modes of operation that can be used:

* normal - Just a plain old call to the LLM, no search, no thinking.
* search - Searches the web and gives the result snippets to the model as context to inform quick, simple and up-to-date queries
* research - Crawls the web for relavant data
* fast code - Crawls the web for code snippets and responds only with a code snippet, usually should use none thinking model
* code - Crawls the web for code snippets and formulates an up-to-date response

Usage:
In the program
```
/mode h
```

### Memories
You local agent remembers your conversations, only if you want it to.

Usage:
In the program
```
/memory h
```

### All commands

Usage:
In the program
```
/help
```


## Setup

### Prerequisits

1. Docker and docker compose installed. You can get it [here](https://docs.docker.com/compose/install)
2. go installed. You can get it [here](https://go.dev/doc/install)
3. Ollama installed with the models of your choosing. You can get it [here](https://ollama.com/download)
   The defaults that work well for me(RTX 3500 ADA laptop with 12GB Vram) are:
    - qwen3:8b with a context window of 40K as the heavy thinking model
    - gemma3:4b with a context window of 128K as the light thinking model 
   The models can be configured using the flags `--light-model` and `--heavy-model` respectively. You can get different models [here](https://ollama.com/search)

### Instructions

Start the search engine - [SearxNG](https://docs.searxng.org/)
```bash
docker compose up -d
```

By default it listens on port 8080

Compile the go binary for your specific system
On Linux and mac
```bash
go build -o YAAP .
```
On Windows
```bash
go build -o YAAP.exe .
```

## To run
To get usage instructions
On Linux and mac
```bash
./YAAP --help
```
On Windows
```bash
./YAAP --help
```
