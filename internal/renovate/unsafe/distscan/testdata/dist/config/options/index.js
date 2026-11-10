const options = [
	{
		name: "allowedCommands",
		allowedValues: ["notThisOne"]
	},
	{
		name: "allowedUnsafeExecutions",
		description: `a description with [brackets] and "quotes"`,
		type: "array",
		allowedValues: [
			"pixi",
			"gradleWrapper"
		]
	}
];
