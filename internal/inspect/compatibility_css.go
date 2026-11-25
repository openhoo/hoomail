package inspect

import (
	"strings"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
)

// Recover at the next declaration after malformed CSS. At EOF (or when a
// malformed token cannot advance) there is nothing more to inspect.
func walkCSS(source string, inline bool, visit func(css.GrammarType, string, []css.Token)) {
	p := css.NewParser(parse.NewInputString(source), inline)
	lastError := -1
	for {
		grammar, _, data := p.Next()
		if grammar == css.ErrorGrammar {
			if !p.HasParseError() || p.Offset() == lastError {
				return
			}
			lastError = p.Offset()
			continue
		}
		visit(grammar, string(data), p.Values())
	}
}

func walkCSSDeclarations(style string, visit func(property string, values []css.Token)) {
	walkCSS(style, true, func(grammar css.GrammarType, property string, values []css.Token) {
		if grammar == css.DeclarationGrammar || grammar == css.CustomPropertyGrammar {
			visit(property, values)
		}
	})
}

func inspectCompatibilityCSS(source string, inline bool, counts map[string]int) {
	seen := map[string]bool{}
	mark := func(slug string) {
		if slug != "" && !seen[slug] {
			seen[slug] = true
			counts[slug]++
		}
	}
	walkCSS(source, inline, func(grammar css.GrammarType, name string, tokens []css.Token) {
		switch grammar {
		case css.BeginRulesetGrammar, css.QualifiedRuleGrammar:
			clear(seen)
			for i := 0; i < len(tokens); i++ {
				if tokens[i].TokenType != css.ColonToken || i+1 == len(tokens) {
					continue
				}
				i++
				double := tokens[i].TokenType == css.ColonToken
				if double && i+1 < len(tokens) {
					i++
				}
				token := tokens[i]
				if token.TokenType != css.IdentToken && token.TokenType != css.FunctionToken {
					continue
				}
				pseudo := strings.ToLower(strings.TrimSuffix(string(token.Data), "("))
				if slug := cssPseudoElements[pseudo]; slug != "" {
					// CSS also allows a single colon for these four CSS2 pseudo-elements.
					if double || pseudo == "before" || pseudo == "after" || pseudo == "first-line" || pseudo == "first-letter" {
						mark(slug)
					}
				} else if !double {
					mark(cssPseudoClasses[pseudo])
				}
			}
		case css.BeginAtRuleGrammar, css.AtRuleGrammar:
			clear(seen)
			mark(cssAtRules[strings.TrimPrefix(name, "@")])
			if name == "@media" {
				for i, token := range tokens {
					if i > 0 && token.TokenType == css.IdentToken && tokens[i-1].TokenType == css.LeftParenthesisToken {
						mark(cssMediaFeatures[strings.ToLower(string(token.Data))])
					}
				}
			}
		case css.EndRulesetGrammar, css.EndAtRuleGrammar:
			clear(seen)
		case css.CustomPropertyGrammar:
			mark("css-variables")
		case css.DeclarationGrammar:
			if len(tokens) == 0 {
				return
			}
			mark(cssProperties[name])
			if name == "inline-size" {
				mark("css-block-inline-size")
			}
			if name == "column-count" {
				mark("css-column-layout-properties")
			}
			if len(tokens) >= 2 && tokens[len(tokens)-2].TokenType == css.DelimToken && string(tokens[len(tokens)-2].Data) == "!" && tokens[len(tokens)-1].TokenType == css.IdentToken && strings.EqualFold(string(tokens[len(tokens)-1].Data), "important") {
				mark("css-important")
				tokens = tokens[:len(tokens)-2]
			}
			if len(tokens) == 1 && tokens[0].TokenType == css.IdentToken {
				value := strings.ToLower(string(tokens[0].Data))
				if name == "display" {
					mark(cssDisplayValues[value])
				}
				if value == "initial" {
					mark("css-unit-initial")
				}
				if cssSizeProperties[name] && (value == "fit-content" || value == "min-content" || value == "max-content") {
					mark("css-intrinsic-size")
				}
			}
			for _, token := range tokens {
				switch token.TokenType {
				case css.DimensionToken:
					n := parse.Number(token.Data)
					mark(cssUnits[strings.ToLower(string(token.Data[n:]))])
				case css.FunctionToken:
					fn := strings.ToLower(strings.TrimSuffix(string(token.Data), "("))
					mark(cssFunctions[fn])
					if fn == "fit-content" && cssSizeProperties[name] {
						mark("css-intrinsic-size")
					}
				}
			}
		}
	})
}
