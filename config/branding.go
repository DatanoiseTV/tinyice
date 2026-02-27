package config

var (
	ThemePrimary       = "#38bdf8"
	ThemePrimaryGlow   = "#0ea5e9"
	ThemeSecondary     = "#a78bfa"
	ThemeSuccess       = "#22c55e"
	ThemeWarning       = "#f59e0b"
	ThemeDanger        = "#ef4444"
	ThemeBackground    = "#09090b"
	ThemeBackgroundAlt = "#18181b"
	ThemeSurface       = "#27272a"
	ThemeBorder        = "#3f3f46"
	ThemeText          = "#fafafa"
	ThemeTextDim       = "#a1a1aa"

	ThemeRadius     = "8px"
	ThemeFontFamily = "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif"
	ThemeFontMono   = "'JetBrains Mono', 'Fira Code', Consolas, monospace"
)

const (
	BrandName = "TinyIce"
	BrandURL  = "https://github.com/DatanoiseTV/tinyice"
)

func ThemeJSConfig() string {
	return `{
		theme: {
			extend: {
				colors: {
					primary: '` + ThemePrimary + `',
					secondary: '` + ThemeSecondary + `',
					accent: '` + ThemeSuccess + `',
					neutral: '` + ThemeSurface + `',
					'base-100': '` + ThemeBackground + `',
					'base-200': '` + ThemeBackgroundAlt + `',
					'base-300': '` + ThemeSurface + `',
					info: '` + ThemePrimary + `',
					success: '` + ThemeSuccess + `',
					warning: '` + ThemeWarning + `',
					error: '` + ThemeDanger + `',
				},
				fontFamily: {
					sans: '` + ThemeFontFamily + `',
					mono: '` + ThemeFontMono + `',
				}
			}
		},
		plugins: [daisyui],
		daisyui: {
			themes: ["tinyice"],
			darkTheme: "tinyice",
		}
	}`
}

func CSSVariables() string {
	return `--primary: ` + ThemePrimary + `;` +
		`--primary-glow: ` + ThemePrimaryGlow + `;` +
		`--secondary: ` + ThemeSecondary + `;` +
		`--success: ` + ThemeSuccess + `;` +
		`--warning: ` + ThemeWarning + `;` +
		`--danger: ` + ThemeDanger + `;` +
		`--bg: ` + ThemeBackground + `;` +
		`--bg-alt: ` + ThemeBackgroundAlt + `;` +
		`--surface: ` + ThemeSurface + `;` +
		`--border: ` + ThemeBorder + `;` +
		`--text: ` + ThemeText + `;` +
		`--text-dim: ` + ThemeTextDim + `;` +
		`--radius: ` + ThemeRadius + `;`
}
