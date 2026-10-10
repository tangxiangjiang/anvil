# Anvil mobile app (Flutter)

Login via **anvil-web**, then call **anvil-scheduler** with the same session Bearer for jobs and models. Fleet ops stay in the web console.

## Prerequisites

- Flutter 3.35+ / Dart 3.9+
- Running `anvil-web` and `anvil-scheduler` with a **shared** sessions DB (`web_sessions_db` on scheduler = web `db_path`)

## Run

```bash
cd app
flutter pub get
flutter run
```

On the login screen:

| Field | Typical local value |
|-------|---------------------|
| anvil-web URL | `http://127.0.0.1:17900` (Android emulator: `http://10.0.2.2:17900`) |
| anvil-scheduler URL | `http://127.0.0.1:17800` (Android emulator: `http://10.0.2.2:17800`) |
| Username / password | Admin from web install wizard |

## Tests

```bash
cd app
flutter test
```
