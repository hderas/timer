document.addEventListener('DOMContentLoaded', () => {
    let timerRunning = false;
    let socket;

    // Dark Mode Toggle Event Listener
    const darkModeToggle = document.getElementById('darkModeToggle');

    if (darkModeToggle) {
        // Load Dark Mode Preference
        if (localStorage.getItem('darkMode') === 'enabled') {
            document.body.classList.add('dark-mode');
            darkModeToggle.checked = true;
        }

        darkModeToggle.addEventListener('change', () => {
            if (darkModeToggle.checked) {
                document.body.classList.add('dark-mode');
                localStorage.setItem('darkMode', 'enabled');
            } else {
                document.body.classList.remove('dark-mode');
                localStorage.setItem('darkMode', 'disabled');
            }
        });
    } else {
        // Apply dark mode if previously enabled
        if (localStorage.getItem('darkMode') === 'enabled') {
            document.body.classList.add('dark-mode');
        }
    }

    // Check if on the main page
    const isMainPage = document.getElementById('statusMessage') !== null;

    if (isMainPage) {
        function updateClock() {
            fetch('/time')
                .then(response => response.json())
                .then(data => {
                    document.getElementById('clock').innerText = data.currentTime;
                })
                .catch(error => {
                    console.error('Error fetching time:', error);
                });
        }

        function updateLogs() {
            fetch('/logs')
                .then(response => {
                    if (!response.ok) {
                        throw new Error(`HTTP error! Status: ${response.status}`);
                    }
                    return response.json();
                })
                .then(data => {
                    if (!data || !Array.isArray(data)) {
                        console.error('Invalid data received from /logs:', data);
                        return;
                    }
                    const logList = document.getElementById('logList');
                    if (!logList) return; // Exit if logList is not present
                    logList.innerHTML = '';
                    data.slice().reverse().forEach(entry => {
                        const listItem = document.createElement('li');
                        listItem.className = 'list-group-item';
                        const eventTime = new Date(entry.time).toLocaleString();
                        let configInfo = '';
                        if (entry.configuration) {
                            configInfo = `
                                <br><small>
                                    Start Time: ${entry.configuration.timestamp},
                                    Day: ${entry.configuration.day},
                                    Match Duration: ${entry.configuration.matchDuration} min,
                                    Pause Duration: ${entry.configuration.pauseDuration} min
                                </small>
                            `;
                        }
                        listItem.innerHTML = `<strong>${entry.event}</strong> at ${eventTime}${configInfo}`;
                        logList.appendChild(listItem);
                    });
                })
                .catch(error => {
                    console.error('Error fetching logs:', error);
                });
        }

        function updateButtonStates() {
            const startButton = document.getElementById('startButton');
            const stopButton = document.getElementById('stopButton');
            const statusMessage = document.getElementById('statusMessage');

            if (!startButton || !stopButton || !statusMessage) return; // Exit if elements are not present

            if (timerRunning) {
                startButton.disabled = true;
                stopButton.disabled = false;
                statusMessage.innerText = 'Timer is running...';
            } else {
                startButton.disabled = false;
                stopButton.disabled = true;
                statusMessage.innerText = 'Timer is stopped.';
            }
        }

        function checkTimerStatus() {
            fetch('/status')
                .then(response => response.json())
                .then(data => {
                    timerRunning = data.timerRunning;
                    updateButtonStates();
                    updateLogs(); // Refresh logs to reflect current state
                })
                .catch(error => {
                    console.error('Error fetching timer status:', error);
                });
        }

        // Start Button Event Listener
        const startButton = document.getElementById('startButton');
        if (startButton) {
            startButton.addEventListener('click', () => {
                const timestampInput = document.getElementById('timestamp').value;
                const dayInput = document.getElementById('day').value;
                const matchDurationInput = parseInt(document.getElementById('matchDuration').value);
                const pauseDurationInput = parseInt(document.getElementById('pauseDuration').value);

                if (!dayInput || !timestampInput) {
                    alert('Please select a valid day and time.');
                    return;
                }

                const config = {
                    timestamp: timestampInput + ":00", // Add seconds
                    day: dayInput,
                    matchDuration: matchDurationInput,
                    pauseDuration: pauseDurationInput
                };

                fetch('/start', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json'
                    },
                    body: JSON.stringify(config)
                })
                .then(response => {
                    if (response.ok) {
                        timerRunning = true;
                        updateButtonStates();
                        updateLogs(); // Update logs immediately
                    } else {
                        response.text().then(text => {
                            alert('Error: ' + text);
                        });
                    }
                })
                .catch(error => {
                    console.error('Error starting timer:', error);
                });
            });
        }

        // Stop Button Event Listener
        const stopButton = document.getElementById('stopButton');
        if (stopButton) {
            stopButton.addEventListener('click', () => {
                fetch('/stop', {
                    method: 'POST',
                })
                .then(response => {
                    if (response.ok) {
                        timerRunning = false;
                        updateButtonStates();
                        updateLogs(); // Update logs immediately
                    } else {
                        response.text().then(text => {
                            alert('Error: ' + text);
                        });
                    }
                })
                .catch(error => {
                    console.error('Error stopping timer:', error);
                });
            });
        }

        // Clear Events Button Event Listener
        const clearLogsButton = document.getElementById('clearLogsButton');
        if (clearLogsButton) {
            clearLogsButton.addEventListener('click', () => {
                if (confirm('Are you sure you want to clear all events?')) {
                    fetch('/clear_logs', {
                        method: 'POST',
                    })
                    .then(response => {
                        if (response.ok) {
                            updateLogs(); // Refresh logs after clearing
                        } else {
                            response.text().then(text => {
                                alert('Error: ' + text);
                            });
                        }
                    })
                    .catch(error => {
                        console.error('Error clearing logs:', error);
                    });
                }
            });
        }

        // Set default date to today
        const dayInput = document.getElementById('day');
        if (dayInput) {
            dayInput.valueAsDate = new Date();
        }

        // Initialize button states
        updateButtonStates();

        // Start updating clock and logs
        setInterval(updateClock, 1000);
        updateClock();

        setInterval(updateLogs, 5000);
        updateLogs();

        // Check timer status when page loads
        checkTimerStatus();

        // WebSocket setup
        function connectWebSocket() {
            if (!window.WebSocket) {
                console.error('WebSocket is not supported by your browser.');
                return;
            }

            const protocol = window.location.protocol === 'https:' ? 'wss://' : 'ws://';
            socket = new WebSocket(`${protocol}${window.location.host}/ws`);

            socket.onopen = () => {
                console.log('WebSocket connection established');
            };

            socket.onmessage = (event) => {
                const data = JSON.parse(event.data);
                handleWebSocketMessage(data);
            };

            socket.onclose = () => {
                console.log('WebSocket connection closed, attempting to reconnect...');
                setTimeout(connectWebSocket, 1000); // Reconnect after 1 second
            };

            socket.onerror = (error) => {
                console.error('WebSocket error:', error);
                socket.close();
            };
        }

        function handleWebSocketMessage(message) {
            if (message.action === 'match_start') {
                playSound('start');
            } else if (message.action === 'match_end') {
                playSound('stop');
            }
        }

        // Function to play sound
        function playSound(type) {
            let soundUrl;
            if (type === 'start') {
                soundUrl = '/static/startkamp.mp3';
            } else if (type === 'stop') {
                soundUrl = '/static/stoppkamp.mp3';
            } else {
                return;
            }

            // Create an AudioContext to comply with autoplay policies
            if (typeof window.AudioContext !== 'undefined' || typeof window.webkitAudioContext !== 'undefined') {
                const audioContext = new (window.AudioContext || window.webkitAudioContext)();
                fetch(soundUrl)
                    .then(response => response.arrayBuffer())
                    .then(arrayBuffer => audioContext.decodeAudioData(arrayBuffer))
                    .then(audioBuffer => {
                        const source = audioContext.createBufferSource();
                        source.buffer = audioBuffer;
                        source.connect(audioContext.destination);
                        source.start(0);
                    })
                    .catch(error => {
                        console.error('Error playing sound:', error);
                    });
            } else {
                // Fallback for older browsers
                const audio = new Audio(soundUrl);
                audio.play().catch(error => {
                    console.error('Error playing sound:', error);
                });
            }
        }

        // Connect to WebSocket
        connectWebSocket();
    }
});
