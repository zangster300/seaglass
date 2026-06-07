const reloader = new EventSource("/reload")

reloader.onmessage = (event) => {
    if (event.data === 'reload') {
        window.location.reload()
    }
}
