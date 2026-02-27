package utils

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

var adjectives = []string{
	// Personality & Vibe
	"Murky", "Silly", "Cool", "Lazy", "Wild", "Calm", "Brave", "Smart", "Kind", "Busy", "Cozy",
	"Dizzy", "Fizzy", "Lucky", "Jolly", "Glad", "Nice", "Fair", "Proud", "Loud", "Quiet",
	"Rich", "Wise", "Pure", "Safe", "Warm", "Zany", "Wacky", "Funky", "Groovy", "Derpy",
	"Cheeky", "Grumpy", "Sleepy", "Hyper", "Manic", "Chill", "Hasty", "Tasty", "Spicy",
	"Salty", "Sweet", "Sour", "Fresh",

	// Physical
	"Big", "Small", "Tiny", "Huge", "Tall", "Short", "Fat", "Thin", "Slim", "Round", "Flat",
	"Wide", "Long", "Deep", "High", "Low", "Soft", "Hard", "Smooth", "Rough", "Fuzzy",
	"Fluffy", "Hairy", "Bald", "Slick", "Shiny", "Dull", "Bright", "Dark", "Clean", "Dirty",
	"Messy", "Neat", "Wet", "Dry",

	// Colors & Visuals
	"Red", "Blue", "Green", "Pink", "Teal", "Cyan", "Gold", "Lime", "Mint", "Rose", "Ruby",
	"Jade", "Neon", "Pale", "Vivid", "Azure", "Amber", "Coral", "Gray", "Black", "White",
	"Snowy", "Sunny", "Cloudy", "Rainy", "Misty", "Icy", "Fiery", "Smoky", "Hazy", "Clear",
	"Blurry",

	// Speed & Power
	"Fast", "Slow", "Swift", "Rapid", "Quick", "Brisk", "Turbo", "Sonic", "Mega", "Giga",
	"Nano", "Pico", "Tera", "Ultra", "Super", "Mighty", "Weak", "Strong", "Heavy", "Light",
	"Bouncy", "Jumpy", "Hoppy",

	// Tech & Abstract
	"Cyber", "Pixel", "Retro", "Modern", "Basic", "Extra", "Beta", "Alpha", "Prime", "Elite",
	"Noble", "Royal", "Regal", "Chief", "Main", "Solo", "Duo", "Trio", "Quad", "Meta",
	"Data", "Binary", "Logic", "Magic", "Secret", "Hidden", "Lost", "Found", "Rare", "Epic",
	"Mythic",

	// Textures/Feel
	"Sticky", "Greasy", "Oily", "Wooden", "Metal", "Plastic", "Paper", "Glass", "Stone",
	"Rocky", "Sandy", "Dusty", "Rusty", "Crusty", "Crispy", "Crunchy",
}

var nouns = []string{
	// Greek / Phonetic (Very common in tech/ops)
	"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Gamma", "Omega", "Sierra",
	"Sigma", "Tango", "Theta", "Victor", "Zeta", "Zulu",

	// Infrastructure & Storage
	"Arch", "Bank", "Base", "Block", "Box", "Cache", "Cell", "Cloud", "Core", "Data", "Deck",
	"Disk", "Dock", "Drive", "Edge", "File", "Grid", "Hub", "Link", "Log", "Mesh", "Net",
	"Node", "Pack", "Pod", "Pool", "Port", "Repo", "Room", "Safe", "Space", "Store",
	"Vault", "Web", "Zone",

	// Teams & Operations
	"Board", "Camp", "Chat", "Crew", "Desk", "Draft", "Force", "Goal", "Group", "Guild",
	"Lab", "Plan", "Project", "Shift", "Squad", "Stage", "Sync", "Task", "Team", "Tier",
	"Unit", "Work",

	// Signals & Abstract Tech
	"Apex", "Axis", "Beam", "Bit", "Byte", "Chain", "Code", "Echo", "Feed", "Flow", "Flux",
	"Hash", "Loop", "Matrix", "Mode", "Nexus", "Note", "Path", "Phase", "Ping", "Prism",
	"Pulse", "Relay", "Route", "Script", "Signal", "Spark", "State", "Stream", "Tensor",
	"Thread", "Track", "Vertex", "Wave", "Wire", "Zenith",
}

func randomItem(words []string) string {
	return words[rand.Intn(len(words))]
}

func randomTwoDigit() string {
	return fmt.Sprintf("%02d", rand.Intn(90)+10)
}

func GenerateRoomName() string {
	rand.Seed(time.Now().UnixNano())
	base := fmt.Sprintf("%s_%s", randomItem(adjectives), randomItem(nouns))
	base = strings.ToLower(base)
	if rand.Float32() < 0.5 {
		return fmt.Sprintf("%s%s", base, randomTwoDigit())
	}
	return base
}
