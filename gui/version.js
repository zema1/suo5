const fs = require("fs")
const manifest = require("./wails.json")
const version = process.argv[2].replace("v", "").split("-")[0]

if (!version) {
    console.log("No version supplied")
    process.exit(1)
}

// update version in wails.json
manifest.info.productVersion = version
const data = JSON.stringify(manifest, null, 2)
fs.writeFileSync("wails.json", data)

// read main.go and replace v0.0.0 with the new version
const mainGoPath = "./main.go"
const mainGoContent = fs.readFileSync(mainGoPath, "utf8")
const newMainGoContent = mainGoContent.replace(/v\d+\.\d+\.\d+/, `v${version}`)
fs.writeFileSync(mainGoPath, newMainGoContent)

console.log(`Updated build version to ${version}`)