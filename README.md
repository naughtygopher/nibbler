[![](https://github.com/naughtygopher/nibbler/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/naughtygopher/nibbler/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/naughtygopher/nibbler.svg)](https://pkg.go.dev/github.com/naughtygopher/nibbler)
[![Go Report Card](https://goreportcard.com/badge/github.com/naughtygopher/nibbler)](https://goreportcard.com/report/github.com/naughtygopher/nibbler)
[![Coverage Status](https://coveralls.io/repos/github/naughtygopher/nibbler/badge.svg?branch=main)](https://coveralls.io/github/naughtygopher/nibbler?branch=main)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/creativecreature/sturdyc/blob/master/LICENSE)

# Nibbler

Nibbler is a minimal package which helps you implement micro-batch processing.

## What is Micro-batch Processing?

Micro-batch processing is a way to handle data by breaking a big task into smaller pieces and processing them one by one. This method is useful in real-time data or streaming situations, wher ,the incoming data is split into "micro-batches" and processed quickly, rather than waiting to collect all data at once.

The same concept can also be extended to handle events processing. So, we have a queue subscriber, and instead of processing the events individually, we create micro batches and process them.

The processing of a single micro batch can be triggered in two ways, based on a time ticker or if the micro batch size is full. i.e. process a non empty batch if duration X has passed or if the batch size is full

## How to use nibbler?

```golang

```
