/* MoveX shared taxonomy (used by the user app AND the admin panel).
   To add a genre / industry / language later: add ONE entry to the matching list below. */
(function (g) {
  var GENRES = ["Action","Adventure","Animation","Biography","Boys Love","Comedy","Crime","Cyberpunk","Dark Fantasy","Detective","Documentary","Drama","Educational","Epic","Family","Fantasy","Film-Noir","Game","Girls Love","Gourmet","Harem","Historical","Horror","Idols","Isekai","Josei","Kids","LGBTQ+","Magic","Martial Arts","Mecha","Medical","Military","Music","Musical","Mystery","Mythology","Parody","Police","Political","Psychological","Reality","Reincarnation","Romance","Romantic Comedy","Samurai","School","Sci-Fi","Seinen","Shoujo","Shounen","Short","Slice of Life","Space","Sports","Super Power","Superhero","Supernatural","Survival","Suspense","Talk Show","Thriller","Time Travel","Tragedy","Vampire","War","Western","Wuxia","Zombie"];
  var POPULAR_GENRES = ["Action","Comedy","Drama","Romance","Thriller","Horror","Fantasy","Sci-Fi","Adventure","Crime"];
  var TYPES = ["Movie","Series","Anime","Web Series","TV Show"];
  var INDUSTRIES = ["Arabic","Australian","Bengali","Bhojpuri","Bollywood","British","Canadian","Chinese","Dhallywood","Filipino","French","German","Gujarati","Hollywood","Hong Kong","Indonesian","Iranian","Italian","Japanese","Kollywood","Korean","Latin American","Lollywood","Marathi","Mollywood","Nollywood","Other","Punjabi","Russian","Sandalwood","Spanish","Taiwanese","Thai","Tollywood","Turkish","Vietnamese"];
  var POPULAR_INDUSTRIES = ["Hollywood","Bollywood","Korean","Japanese","Tollywood","Kollywood"];
  var LANGUAGES = ["Hindi","Bengali","Arabic","English","French","Japanese","Kannada","Korean","Malayalam","Marathi","Punjabi","Russian","Spanish","Tamil","Telugu","Thai","Turkish","Urdu","Chinese"];
  var POPULAR_LANGUAGES = ["Hindi","Bengali"];

  var lower = function (s) { return String(s || '').trim().toLowerCase(); };
  var toArr = function (v) { return Array.isArray(v) ? v.filter(Boolean) : (v && typeof v === 'object' ? Object.values(v).filter(Boolean) : []); };

  var MX = {
    GENRES: GENRES, POPULAR_GENRES: POPULAR_GENRES, TYPES: TYPES,
    INDUSTRIES: INDUSTRIES, POPULAR_INDUSTRIES: POPULAR_INDUSTRIES,
    LANGUAGES: LANGUAGES, POPULAR_LANGUAGES: POPULAR_LANGUAGES,

    /* genres of a title: new `genres` list, or old single `category` split into known genres (e.g. "Anime-Action") */
    genresOf: function (m) {
      var own = toArr(m && m.genres);
      if (own.length) return own;
      var c = lower(m && m.category);
      if (!c) return [];
      var out = GENRES.filter(function (x) { return c === lower(x) || (' ' + c.replace(/[-,\/&+]/g, ' ') + ' ').indexOf(' ' + lower(x) + ' ') >= 0; });
      return out;
    },
    isSeries: function (m) { return !!m && m.contentType === 'series'; },
    typeOf: function (m) { return (m && m.type) || (m && m.contentType === 'series' ? 'Series' : 'Movie'); },
    /* which home tabs a title belongs to */
    tabsOf: function (m) {
      var t = [m && m.contentType === 'series' ? 'series' : 'movies'];
      var ty = lower(m && m.type);
      if (ty === 'anime') t.push('anime');
      return t;
    },
    langsOf: function (m) { return toArr(m && m.languages); },
    langBadge: function (m) { var l = MX.langsOf(m); return l.length > 1 ? 'Multi Audio' : (l[0] || ''); },
    /* rating shown only when it is a real number (hides "Loading..." / "N/A") */
    rating: function (m) { var n = parseFloat(m && m.rating); return isFinite(n) && n > 0 ? (Math.round(n * 10) / 10).toString() : ''; },
    ratingNum: function (m) { var n = parseFloat(m && m.rating); return isFinite(n) ? n : 0; },
    isHttpUrl: function (u) { return /^https?:\/\//i.test(String(u || '')); }
  };
  g.MX = MX;
})(window);
