const fs = require("fs")
const manifest = require("./wails.json")
const version = process.argv[2].replace("v", "").split("-")[0]

if (!version) {
    console.log("No version supplied")
    process.exit(1)
}

manifest.info.productVersion = version

const data = JSON.stringify(manifest, null, 2)
fs.writeFileSync("wails.json", data)
console.log(`Updated build version to ${version}`)