function generate() {
    const blessings = [
        "May you find strength in every wave you face",
        "May your heart be anchored in hope",
        "May calm waters and clear skies find you often",
        "May each sunrise bring you new horizons to explore",
    ]

    const idx = Math.floor(Math.random() * blessings.length)
    return blessings[idx]
}

const blessing = document.createElement('main')
document.body.appendChild(blessing)

const mainEl = document.querySelector('main')
mainEl.innerText = generate()

setInterval(() => {
    mainEl.innerText = generate()
}, 5000)



