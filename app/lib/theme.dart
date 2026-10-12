import 'package:flutter/material.dart';

/// Matches anvil-web `app.css` tokens.
abstract final class AnvilColors {
  static const bg = Color(0xFF0F1419);
  static const panel = Color(0xFF1A222C);
  static const sidebar = Color(0xFF121820);
  static const border = Color(0xFF2C3847);
  static const text = Color(0xFFE7EEF7);
  static const muted = Color(0xFF8B9BB0);
  static const accent = Color(0xFF3D9CF0);
  static const accentDim = Color(0xFF2563A8);
  static const ok = Color(0xFF3ECF8E);
  static const warn = Color(0xFFE6A23C);
  static const danger = Color(0xFFE85D5D);
}

ThemeData anvilTheme() {
  const scheme = ColorScheme.dark(
    surface: AnvilColors.bg,
    onSurface: AnvilColors.text,
    surfaceContainerHighest: AnvilColors.panel,
    primary: AnvilColors.accent,
    onPrimary: AnvilColors.text,
    secondary: AnvilColors.accentDim,
    onSecondary: AnvilColors.text,
    error: AnvilColors.danger,
    onError: AnvilColors.text,
    outline: AnvilColors.border,
  );
  return ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    colorScheme: scheme,
    scaffoldBackgroundColor: AnvilColors.bg,
    canvasColor: AnvilColors.bg,
    cardColor: AnvilColors.panel,
    dividerColor: AnvilColors.border,
    appBarTheme: const AppBarTheme(
      backgroundColor: AnvilColors.sidebar,
      foregroundColor: AnvilColors.text,
      elevation: 0,
      centerTitle: false,
    ),
    cardTheme: CardThemeData(
      color: AnvilColors.panel,
      elevation: 0,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: AnvilColors.border),
      ),
      margin: EdgeInsets.zero,
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: AnvilColors.accentDim,
        foregroundColor: AnvilColors.text,
        side: const BorderSide(color: AnvilColors.accent),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: AnvilColors.text,
        side: const BorderSide(color: AnvilColors.border),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
      ),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: AnvilColors.panel,
      labelStyle: const TextStyle(color: AnvilColors.muted),
      hintStyle: const TextStyle(color: AnvilColors.muted),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(8),
        borderSide: const BorderSide(color: AnvilColors.border),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(8),
        borderSide: const BorderSide(color: AnvilColors.accent),
      ),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(8),
        borderSide: const BorderSide(color: AnvilColors.border),
      ),
    ),
    listTileTheme: const ListTileThemeData(
      iconColor: AnvilColors.muted,
      textColor: AnvilColors.text,
    ),
    progressIndicatorTheme: const ProgressIndicatorThemeData(
      color: AnvilColors.accent,
    ),
    snackBarTheme: SnackBarThemeData(
      backgroundColor: AnvilColors.panel,
      contentTextStyle: const TextStyle(color: AnvilColors.text),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
    ),
    navigationBarTheme: NavigationBarThemeData(
      backgroundColor: AnvilColors.sidebar,
      indicatorColor: AnvilColors.panel,
      labelTextStyle: WidgetStateProperty.resolveWith((states) {
        final selected = states.contains(WidgetState.selected);
        return TextStyle(
          color: selected ? AnvilColors.text : AnvilColors.muted,
          fontSize: 12,
        );
      }),
      iconTheme: WidgetStateProperty.resolveWith((states) {
        final selected = states.contains(WidgetState.selected);
        return IconThemeData(
          color: selected ? AnvilColors.accent : AnvilColors.muted,
        );
      }),
    ),
  );
}
