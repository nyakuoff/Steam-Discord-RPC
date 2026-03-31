# Steam Discord RPC (Linux)

A Go wrapper for Steam launch options that sets Discord Rich Presence automatically for every game.

What it does:
- Reads the game name and app ID from Steam.
- Fetches the game icon from SteamGridDB.
- Sets Rich Presence before launch and clears it when the game exits.

## Install

```bash
git clone <repo>
cd SteamDiscordRPC
./install.sh
```

This builds the binary, installs it to `~/.local/bin`, and copies the example config to `~/.config/steamdiscordrpc/config.json` if one doesn't exist yet.

## Steam launch option

In Steam → right-click game → Properties → Launch Options:

```
steamdiscordrpc %command%
```

## Setup

### 1. Discord application

Create an application at https://discord.com/developers/applications and copy the **Application ID**.

### 2. SteamGridDB API key

Get a free API key at https://www.steamgriddb.com/profile/preferences/api

### 3. Config file

Default location: `~/.config/steamdiscordrpc/config.json`

```bash
mkdir -p ~/.config/steamdiscordrpc
```

Minimal config:

```json
{
  "steamgriddb_api_key": "your_key_here",
  "default": {
    "client_id": "your_discord_app_id",
    "details": "Playing {game_name}",
    "state": "",
    "large_text": "{game_name}",
    "small_image": "",
    "small_text": "",
    "no_timestamp": false
  }
}
```

Custom config path:

```
~/.local/bin/steamdiscordrpc --config /path/to/config.json %command%
```
