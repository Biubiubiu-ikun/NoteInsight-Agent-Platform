# Planning Precedence

## Current Rule

The project now uses `最新项目规划.md` as the primary planning document.

Files whose names start with `旧版项目规划` remain useful as historical context and for reusable engineering ideas, but whenever planning documents differ, `最新项目规划.md` takes precedence.

## Domain Direction

The project domain is moving from a medium/long-video creator insight platform to a Xiaohongshu-style image-text note community:

- `videos` becomes `notes`
- `comments` becomes `note_comments`
- `danmus` are removed
- image captions and OCR text are modeled with `note_media`
- note likes, collects, shares, and comment likes become core interactions
