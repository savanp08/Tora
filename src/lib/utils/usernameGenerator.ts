const adjectives = [
	'Lazy',
	'Silly',
	'Fat',
	'Grumpy',
	'Happy',
	'Dizzy',
	'Crazy',
	'Sleepy',
	'Nasty',
	'Fancy',
	'Chubby',
	'Fluffy'
];

const animals = [
	'Cat',
	'Goose',
	'Rabbit',
	'Panda',
	'Dog',
	'Hamster',
	'Turtle',
	'Badger',
	'Fox',
	'Owl',
	'Sloth',
	'Yak'
];

function randomItem(items: string[]) {
	return items[Math.floor(Math.random() * items.length)];
}

export function generateUsername() {
	return `${randomItem(adjectives)}_${randomItem(animals)}`;
}
