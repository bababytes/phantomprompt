# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

First public release. PhantomPrompt for macOS masks VINs, e-mail addresses, GUIDs, labelled fields, known secret formats, passwords in URLs and after labels (`DB_PASSWORD=…`, `password: "…"`), and your own patterns (except in folders you trust with `phantom trust`, such as test fixtures) before Claude Code, Codex CLI and GitHub Copilot CLI send them to a model, and restores the real values in answers; the token map lives in memory only. It ships as a DMG with the app, the local panel and protected terminals (`phantom`), plus CLI ZIPs for Apple silicon and Intel Macs.
