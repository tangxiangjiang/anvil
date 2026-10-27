/** Flattens VS Code's single or sectioned configuration contribution. */
export function configurationProperties(pkg: {
  contributes: { configuration: unknown };
}): Record<string, any> {
  const raw = pkg.contributes.configuration;
  const sections = Array.isArray(raw) ? raw : [raw];
  return Object.assign(
    {},
    ...sections.map((section) =>
      section && typeof section === 'object'
        ? ((section as { properties?: Record<string, any> }).properties ?? {})
        : {},
    ),
  );
}
