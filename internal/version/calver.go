package version

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultCalverFormat is used for calver packages that don't set a format.
const DefaultCalverFormat = "YYYY.0M.MICRO"

const (
	tokenMicro       = "MICRO"
	tokenWeek        = "WW"
	tokenPaddedWeek  = "0W"
	patternOneOrTwo  = `\d{1,2}`
	patternTwoDigits = `\d{2}`
)

// Significance of each kind of token, from most to least, for ordering versions.
const (
	rankYear = iota
	rankMonth
	rankWeek
	rankDay
	rankMicro
)

// calverToken is one of the calver.org format tokens.
type calverToken struct {
	name    string
	pattern string
	padded  bool
	rank    int
	value   func(dt calverDate) int
}

type calverDate struct {
	year, month, isoWeek, day int
}

// calverTokens is ordered so that longer tokens match before their prefixes.
var calverTokens = []calverToken{
	{name: tokenMicro, pattern: `\d+`, rank: rankMicro},
	{name: "YYYY", pattern: `\d{4}`, rank: rankYear, value: func(dt calverDate) int { return dt.year }},
	{name: "YY", pattern: `\d{1,3}`, rank: rankYear, value: func(dt calverDate) int { return dt.year - 2000 }},
	{
		name:    "0Y",
		pattern: `\d{2,3}`,
		padded:  true,
		rank:    rankYear,
		value:   func(dt calverDate) int { return dt.year - 2000 },
	},
	{name: "MM", pattern: patternOneOrTwo, rank: rankMonth, value: func(dt calverDate) int { return dt.month }},
	{
		name:    "0M",
		pattern: patternTwoDigits,
		padded:  true,
		rank:    rankMonth,
		value:   func(dt calverDate) int { return dt.month },
	},
	{name: tokenWeek, pattern: patternOneOrTwo, rank: rankWeek, value: func(dt calverDate) int { return dt.isoWeek }},
	{
		name:    tokenPaddedWeek,
		pattern: patternTwoDigits,
		padded:  true,
		rank:    rankWeek,
		value:   func(dt calverDate) int { return dt.isoWeek },
	},
	{name: "DD", pattern: patternOneOrTwo, rank: rankDay, value: func(dt calverDate) int { return dt.day }},
	{
		name:    "0D",
		pattern: patternTwoDigits,
		padded:  true,
		rank:    rankDay,
		value:   func(dt calverDate) int { return dt.day },
	},
}

// calverPart is either a token or a literal run of the format.
type calverPart struct {
	token   *calverToken
	literal string
}

type calver struct {
	format string
	parts  []calverPart
	// bySignificance are the format's tokens, most significant first.
	bySignificance []*calverToken
	pattern        *regexp.Regexp
	hasMicro       bool
	weekly         bool
}

// NewCalver returns a calendar versioning scheme using calver.org tokens, such as "YYYY.0M.MICRO".
//
// Any release takes its date parts from the release date.
// MICRO counts releases within the same date parts, starting at 0.
// When the format uses a week token, years are ISO week-numbering years, so a week never spans two years.
func NewCalver(format string) (Scheme, error) {
	if format == "" {
		format = DefaultCalverFormat
	}
	parts := splitCalverFormat(format)

	var pattern strings.Builder
	pattern.WriteString("^")
	seen := map[string]bool{}
	cal := &calver{format: format, parts: parts}
	for index, part := range parts {
		if part.token == nil {
			pattern.WriteString(regexp.QuoteMeta(part.literal))
			continue
		}
		name := part.token.name
		if seen[name] {
			return nil, fmt.Errorf("%w: calver format %q repeats %s", ErrInvalid, format, name)
		}
		seen[name] = true
		if index > 0 && parts[index-1].token != nil {
			return nil, fmt.Errorf("%w: calver format %q needs a separator before %s", ErrInvalid, format, name)
		}
		cal.bySignificance = append(cal.bySignificance, part.token)
		cal.hasMicro = cal.hasMicro || name == tokenMicro
		cal.weekly = cal.weekly || name == tokenWeek || name == tokenPaddedWeek
		pattern.WriteString("(" + part.token.pattern + ")")
	}
	pattern.WriteString("$")

	if len(seen) == 0 || (len(seen) == 1 && cal.hasMicro) {
		return nil, fmt.Errorf("%w: calver format %q has no date tokens", ErrInvalid, format)
	}
	cal.pattern = regexp.MustCompile(pattern.String())
	slices.SortStableFunc(cal.bySignificance, func(a, b *calverToken) int { return cmp.Compare(a.rank, b.rank) })
	return cal, nil
}

func splitCalverFormat(format string) []calverPart {
	var parts []calverPart
	var literal strings.Builder
	for rest := format; rest != ""; {
		token := matchCalverToken(rest)
		if token == nil {
			literal.WriteByte(rest[0])
			rest = rest[1:]
			continue
		}
		if literal.Len() > 0 {
			parts = append(parts, calverPart{literal: literal.String()})
			literal.Reset()
		}
		parts = append(parts, calverPart{token: token})
		rest = rest[len(token.name):]
	}
	if literal.Len() > 0 {
		parts = append(parts, calverPart{literal: literal.String()})
	}
	return parts
}

func matchCalverToken(s string) *calverToken {
	for i := range calverTokens {
		if strings.HasPrefix(s, calverTokens[i].name) {
			return &calverTokens[i]
		}
	}
	return nil
}

func (c *calver) Initial(now time.Time) (string, error) {
	return c.render(c.date(now), 0), nil
}

func (c *calver) Validate(v string) error {
	_, err := c.parse(v)
	return err
}

func (c *calver) Next(current string, bump Bump, now time.Time) (string, error) {
	if bump == None {
		return current, nil
	}
	values, err := c.parse(current)
	if err != nil {
		return "", err
	}
	today := c.date(now)
	if !c.sameDate(values, today) {
		return c.render(today, 0), nil
	}
	if !c.hasMicro {
		return "", fmt.Errorf("%w: %s was already released for this date and format %q has no MICRO",
			ErrInvalid, current, c.format)
	}
	return c.render(today, values[tokenMicro]+1), nil
}

func (c *calver) Compare(a, b string) (int, error) {
	valuesA, err := c.parse(a)
	if err != nil {
		return 0, err
	}
	valuesB, err := c.parse(b)
	if err != nil {
		return 0, err
	}
	for _, token := range c.bySignificance {
		if order := cmp.Compare(valuesA[token.name], valuesB[token.name]); order != 0 {
			return order, nil
		}
	}
	return 0, nil
}

func (c *calver) date(now time.Time) calverDate {
	isoYear, isoWeek := now.ISOWeek()
	dt := calverDate{year: now.Year(), month: int(now.Month()), isoWeek: isoWeek, day: now.Day()}
	if c.weekly {
		dt.year = isoYear
	}
	return dt
}

// parse returns the value of each token in a version.
func (c *calver) parse(ver string) (map[string]int, error) {
	match := c.pattern.FindStringSubmatch(ver)
	if match == nil {
		return nil, fmt.Errorf("%w: %q does not match calver format %q", ErrInvalid, ver, c.format)
	}
	values := map[string]int{}
	group := 1
	for _, part := range c.parts {
		if part.token == nil {
			continue
		}
		n, err := strconv.Atoi(match[group])
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrInvalid, ver, err)
		}
		values[part.token.name] = n
		group++
	}
	return values, nil
}

func (c *calver) sameDate(values map[string]int, today calverDate) bool {
	for _, part := range c.parts {
		if part.token == nil || part.token.name == tokenMicro {
			continue
		}
		if values[part.token.name] != part.token.value(today) {
			return false
		}
	}
	return true
}

func (c *calver) render(today calverDate, micro int) string {
	var out strings.Builder
	for _, part := range c.parts {
		switch {
		case part.token == nil:
			out.WriteString(part.literal)
		case part.token.name == tokenMicro:
			out.WriteString(strconv.Itoa(micro))
		case part.token.padded:
			fmt.Fprintf(&out, "%02d", part.token.value(today))
		default:
			out.WriteString(strconv.Itoa(part.token.value(today)))
		}
	}
	return out.String()
}
