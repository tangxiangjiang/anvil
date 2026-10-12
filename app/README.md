# Anvil mobile app (Flutter)

Web URL is **baked in at build time** (`ANVIL_WEB_URL`). Sign in with username/password, pick a scheduler **fleet** from web, then operate that cluster (nodes scale/drain), jobs, and models with the web session Bearer.

## Prerequisites

- Flutter 3.35+ / Dart 3.9+
- Running `anvil-web` and `anvil-scheduler` with a **shared** sessions DB (`web_sessions_db` on scheduler = web `db_path`)

From the repo root, the easiest way is:

```bash
python scripts/dev_stack.py
```

## Run

```bash
cd app
flutter pub get

# default web URL: http://127.0.0.1:17900
flutter run

# Android emulator / LAN / production package:
flutter run --dart-define=ANVIL_WEB_URL=http://10.0.2.2:17900
flutter build apk --dart-define=ANVIL_WEB_URL=https://web.example.com
```

Login screen: username / password only. After login, pick a fleet (scheduler URL from web `fleets:`).

## Tests

```bash
cd app
flutter test
```
