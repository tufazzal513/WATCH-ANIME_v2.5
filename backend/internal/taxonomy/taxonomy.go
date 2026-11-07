// Package taxonomy holds the fixed lists used by the admin form and the user app.
// It is an exact port of assets/js/shared/taxonomy.js. To add a genre/industry/language later, add one entry here.
package taxonomy

var (
	Genres = []string{
		"Action",
		"Adventure",
		"Animation",
		"Biography",
		"Boys Love",
		"Comedy",
		"Crime",
		"Cyberpunk",
		"Dark Fantasy",
		"Detective",
		"Documentary",
		"Drama",
		"Educational",
		"Epic",
		"Family",
		"Fantasy",
		"Film-Noir",
		"Game",
		"Girls Love",
		"Gourmet",
		"Harem",
		"Historical",
		"Horror",
		"Idols",
		"Isekai",
		"Josei",
		"Kids",
		"LGBTQ+",
		"Magic",
		"Martial Arts",
		"Mecha",
		"Medical",
		"Military",
		"Music",
		"Musical",
		"Mystery",
		"Mythology",
		"Parody",
		"Police",
		"Political",
		"Psychological",
		"Reality",
		"Reincarnation",
		"Romance",
		"Romantic Comedy",
		"Samurai",
		"School",
		"Sci-Fi",
		"Seinen",
		"Shoujo",
		"Shounen",
		"Short",
		"Slice of Life",
		"Space",
		"Sports",
		"Super Power",
		"Superhero",
		"Supernatural",
		"Survival",
		"Suspense",
		"Talk Show",
		"Thriller",
		"Time Travel",
		"Tragedy",
		"Vampire",
		"War",
		"Western",
		"Wuxia",
		"Zombie",
	}

	PopularGenres = []string{
		"Action",
		"Comedy",
		"Drama",
		"Romance",
		"Thriller",
		"Horror",
		"Fantasy",
		"Sci-Fi",
		"Adventure",
		"Crime",
	}

	Types = []string{
		"Movie",
		"Series",
		"Anime",
		"Web Series",
		"TV Show",
	}

	Industries = []string{
		"Arabic",
		"Australian",
		"Bengali",
		"Bhojpuri",
		"Bollywood",
		"British",
		"Canadian",
		"Chinese",
		"Dhallywood",
		"Filipino",
		"French",
		"German",
		"Gujarati",
		"Hollywood",
		"Hong Kong",
		"Indonesian",
		"Iranian",
		"Italian",
		"Japanese",
		"Kollywood",
		"Korean",
		"Latin American",
		"Lollywood",
		"Marathi",
		"Mollywood",
		"Nollywood",
		"Other",
		"Punjabi",
		"Russian",
		"Sandalwood",
		"Spanish",
		"Taiwanese",
		"Thai",
		"Tollywood",
		"Turkish",
		"Vietnamese",
	}

	PopularIndustries = []string{
		"Hollywood",
		"Bollywood",
		"Korean",
		"Japanese",
		"Tollywood",
		"Kollywood",
	}

	Languages = []string{
		"Hindi",
		"Bengali",
		"Arabic",
		"English",
		"French",
		"Japanese",
		"Kannada",
		"Korean",
		"Malayalam",
		"Marathi",
		"Punjabi",
		"Russian",
		"Spanish",
		"Tamil",
		"Telugu",
		"Thai",
		"Turkish",
		"Urdu",
		"Chinese",
	}

	PopularLanguages = []string{
		"Hindi",
		"Bengali",
	}
)

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// IsType reports whether v is an allowed content type label (Movie, Series, Anime, ...).
func IsType(v string) bool { return contains(Types, v) }

// IsIndustry reports whether v is an allowed industry.
func IsIndustry(v string) bool { return contains(Industries, v) }

// IsLanguage reports whether v is an allowed language.
func IsLanguage(v string) bool { return contains(Languages, v) }

// IsPopularGenre reports whether the genre is shown as a quick-pick chip.
func IsPopularGenre(v string) bool { return contains(PopularGenres, v) }
