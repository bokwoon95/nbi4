: << '::HEREDOC'
@ECHO OFF
set CGO_ENABLED=0
@ECHO ON
wgo -xdir . npx @tailwindcss/cli --input ./static/notebrew.css --output ./static/notebrew.min.css --watch=always :: ^
wgo -xdir . npx esbuild ./static/notebrew.js --bundle --outfile=./static/notebrew.min.js --watch=forever :: ^
wgo -verbose -file .go go install -tags dev ./notebrew :: ^
notebrew
@ECHO OFF
GOTO ONE
::HEREDOC

CGO_ENABLED=0
(
set -x
wgo -xdir . npx @tailwindcss/cli --input ./static/notebrew.css --output ./static/notebrew.min.css --watch=always "::" \
wgo -xdir . npx esbuild ./static/notebrew.js --bundle --outfile=./static/notebrew.min.js --watch=forever "::" \
wgo -verbose -file .go go install -tags dev ./notebrew "::" \
notebrew
)

: << '::HEREDOC'
:ONE
::HEREDOC
