/*
 * video.js — Vanilla JS P2P video chat for small groups.
 * Mesh topology for ≤8 participants. No frameworks, no dependencies.
 * SPDX-License-Identifier: AGPL-3.0
 */
(function () {
  'use strict';

  // ---- Config (injected by template) ----
  var GROUP_KEY = window.VIDEO_CONFIG.groupKey;
  var PEER_ID = window.VIDEO_CONFIG.peerId;
  var PEER_NAME = window.VIDEO_CONFIG.peerName;
  var WS_URL = window.VIDEO_CONFIG.wsUrl;
  var MAX_PARTICIPANTS = 8;

  // ---- State ----
  var ws = null;
  var localStream = null;
  var screenStream = null;
  var peerConnections = {}; // peerId → RTCPeerConnection
  var peerNames = {};        // peerId → display name
  var micEnabled = true;
  var camEnabled = true;
  var screenSharing = false;

  // ---- DOM ----
  var grid = document.getElementById('video-grid');
  var participantList = document.getElementById('participant-list');
  var callStatus = document.getElementById('call-status');
  var btnMute = document.getElementById('btn-mute');
  var btnCam = document.getElementById('btn-cam');
  var btnScreen = document.getElementById('btn-screen');
  var btnLeave = document.getElementById('btn-leave');

  // ---- WebRTC config (no STUN/TURN — direct behind Tailscale) ----
  var rtcConfig = { iceServers: [] };

  // ---- Init ----
  function init() {
    navigator.mediaDevices.getUserMedia({ video: true, audio: true })
      .then(function (stream) {
        localStream = stream;
        addLocalTile(PEER_ID, PEER_NAME, stream);
        connectWebSocket();
      })
      .catch(function (err) {
        console.error('Failed to get media:', err);
        // Try audio-only
        return navigator.mediaDevices.getUserMedia({ video: false, audio: true });
      })
      .then(function (stream) {
        if (stream && !localStream) {
          localStream = stream;
          camEnabled = false;
          if (btnCam) btnCam.classList.add('btn-off');
          addLocalTile(PEER_ID, PEER_NAME, stream);
          connectWebSocket();
        }
      })
      .catch(function (err) {
        console.error('No media access:', err);
        if (callStatus) callStatus.textContent = 'Camera/mic access denied';
      });

    // Button handlers
    if (btnMute) btnMute.addEventListener('click', toggleMic);
    if (btnCam) btnCam.addEventListener('click', toggleCam);
    if (btnScreen) btnScreen.addEventListener('click', toggleScreenShare);
    if (btnLeave) btnLeave.addEventListener('click', leaveCall);
  }

  // ---- WebSocket signaling ----
  function connectWebSocket() {
    ws = new WebSocket(WS_URL);

    ws.onopen = function () {
      if (callStatus) callStatus.textContent = 'Connected';
      if (callStatus) callStatus.className = 'call-status connected';
    };

    ws.onmessage = function (event) {
      var msg = JSON.parse(event.data);
      handleSignal(msg);
    };

    ws.onclose = function () {
      if (callStatus) callStatus.textContent = 'Disconnected';
      if (callStatus) callStatus.className = 'call-status disconnected';
      // Don't auto-reconnect — user can reload
    };

    ws.onerror = function () {
      if (callStatus) callStatus.textContent = 'Connection error';
    };
  }

  // ---- Signal handler ----
  function handleSignal(msg) {
    switch (msg.type) {
      case 'peers':
        // Received list of existing peers — initiate connections to each
        for (var i = 0; i < msg.peers.length; i++) {
          var p = msg.peers[i];
          peerNames[p.id] = p.name;
          initiateConnection(p.id, p.name);
        }
        updateParticipantList();
        break;

      case 'join':
        // New peer joined — they will initiate the connection
        peerNames[msg.from] = msg.name;
        updateParticipantList();
        break;

      case 'offer':
        handleOffer(msg);
        break;

      case 'answer':
        handleAnswer(msg);
        break;

      case 'ice':
        handleICE(msg);
        break;

      case 'leave':
        removePeer(msg.from);
        break;

      case 'mute_all':
        // Mute our mic (leader requested mute all)
        if (micEnabled) toggleMic();
        break;

      case 'error':
        if (callStatus) callStatus.textContent = msg.name || 'Error';
        if (callStatus) callStatus.className = 'call-status disconnected';
        break;
    }
  }

  // ---- Connection management (mesh: each peer connects to every other) ----
  function initiateConnection(peerId, peerName) {
    if (peerConnections[peerId]) return;
    var pc = createPeerConnection(peerId);
    peerConnections[peerId] = pc;

    // Add local tracks
    if (localStream) {
      localStream.getTracks().forEach(function (track) {
        pc.addTrack(track, localStream);
      });
    }

    // Create and send offer
    pc.createOffer().then(function (offer) {
      return pc.setLocalDescription(offer);
    }).then(function () {
      sendSignal({ type: 'offer', to: peerId, sdp: pc.localDescription.sdp });
    }).catch(function (err) {
      console.error('Create offer failed for', peerId, err);
    });
  }

  function createPeerConnection(peerId) {
    var pc = new RTCPeerConnection(rtcConfig);

    // ICE candidates
    pc.onicecandidate = function (event) {
      if (event.candidate) {
        sendSignal({
          type: 'ice',
          to: peerId,
          ice: event.candidate.candidate,
          ice_label: event.candidate.sdpMLineIndex != null ? String(event.candidate.sdpMLineIndex) : ''
        });
      }
    };

    // Remote stream
    pc.ontrack = function (event) {
      addRemoteTile(peerId, peerNames[peerId] || 'Peer', event.streams[0]);
    };

    // Connection state
    pc.onconnectionstatechange = function () {
      updateConnStatus(peerId, pc.connectionState);
    };

    return pc;
  }

  function handleOffer(msg) {
    var peerId = msg.from;
    if (!peerConnections[peerId]) {
      var pc = createPeerConnection(peerId);
      peerConnections[peerId] = pc;
      if (localStream) {
        localStream.getTracks().forEach(function (track) {
          pc.addTrack(track, localStream);
        });
      }
    }

    var pc = peerConnections[peerId];
    pc.setRemoteDescription({ type: 'offer', sdp: msg.sdp }).then(function () {
      return pc.createAnswer();
    }).then(function (answer) {
      return pc.setLocalDescription(answer);
    }).then(function () {
      sendSignal({ type: 'answer', to: peerId, sdp: pc.localDescription.sdp });
    }).catch(function (err) {
      console.error('Handle offer failed for', peerId, err);
    });
  }

  function handleAnswer(msg) {
    var pc = peerConnections[msg.from];
    if (pc) {
      pc.setRemoteDescription({ type: 'answer', sdp: msg.sdp }).catch(function (err) {
        console.error('Set remote answer failed for', msg.from, err);
      });
    }
  }

  function handleICE(msg) {
    var pc = peerConnections[msg.from];
    if (pc) {
      var candidate = new RTCIceCandidate({
        candidate: msg.ice,
        sdpMLineIndex: msg.ice_label ? parseInt(msg.ice_label, 10) : 0
      });
      pc.addIceCandidate(candidate).catch(function (err) {
        console.error('Add ICE failed for', msg.from, err);
      });
    }
  }

  // ---- Send signal ----
  function sendSignal(msg) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(msg));
    }
  }

  // ---- UI: video tiles ----
  function addLocalTile(id, name, stream) {
    var tile = createTile(id, name, true);
    var video = tile.querySelector('video');
    video.srcObject = stream;
    video.muted = true; // don't hear ourselves
    updateParticipantList();
  }

  function addRemoteTile(id, name, stream) {
    var existing = document.getElementById('tile-' + id);
    if (existing) {
      existing.querySelector('video').srcObject = stream;
      return;
    }
    var tile = createTile(id, name, false);
    tile.querySelector('video').srcObject = stream;
    updateParticipantList();
  }

  function createTile(id, name, isLocal) {
    var tile = document.createElement('div');
    tile.className = 'video-tile';
    tile.id = 'tile-' + id;

    var video = document.createElement('video');
    video.autoplay = true;
    video.playsInline = true;
    tile.appendChild(video);

    var overlay = document.createElement('div');
    overlay.className = 'tile-overlay';
    overlay.textContent = (isLocal ? name + ' (You)' : name);
    tile.appendChild(overlay);

    var indicator = document.createElement('div');
    indicator.className = 'conn-indicator';
    indicator.id = 'conn-' + id;
    indicator.textContent = '●';
    tile.appendChild(indicator);

    grid.appendChild(tile);
    return tile;
  }

  function removePeer(peerId) {
    if (peerConnections[peerId]) {
      peerConnections[peerId].close();
      delete peerConnections[peerId];
    }
    delete peerNames[peerId];
    var tile = document.getElementById('tile-' + peerId);
    if (tile) tile.remove();
    updateParticipantList();
  }

  function updateConnStatus(peerId, state) {
    var indicator = document.getElementById('conn-' + peerId);
    if (!indicator) return;
    var color = '#888';
    if (state === 'connected') color = 'var(--success)';
    else if (state === 'connecting' || state === 'new') color = 'var(--warning)';
    else if (state === 'failed' || state === 'disconnected' || state === 'closed') color = 'var(--danger)';
    indicator.style.color = color;
    indicator.title = state;
  }

  // ---- Participant list ----
  function updateParticipantList() {
    if (!participantList) return;
    var count = 1; // self
    for (var id in peerNames) count++;
    participantList.innerHTML = '<strong>' + count + ' in call</strong>';
  }

  // ---- Controls ----
  function toggleMic() {
    if (!localStream) return;
    micEnabled = !micEnabled;
    localStream.getAudioTracks().forEach(function (t) { t.enabled = micEnabled; });
    if (btnMute) {
      btnMute.classList.toggle('btn-off', !micEnabled);
      btnMute.title = micEnabled ? 'Mute' : 'Unmute';
    }
  }

  function toggleCam() {
    if (!localStream) return;
    camEnabled = !camEnabled;
    localStream.getVideoTracks().forEach(function (t) { t.enabled = camEnabled; });
    if (btnCam) {
      btnCam.classList.toggle('btn-off', !camEnabled);
      btnCam.title = camEnabled ? 'Disable camera' : 'Enable camera';
    }
  }

  function toggleScreenShare() {
    if (screenSharing) {
      stopScreenShare();
    } else {
      startScreenShare();
    }
  }

  function startScreenShare() {
    navigator.mediaDevices.getDisplayMedia({ video: true }).then(function (stream) {
      screenStream = stream;
      screenSharing = true;
      if (btnScreen) btnScreen.classList.add('btn-on');
      // Replace video track in all peer connections
      var screenTrack = stream.getVideoTracks()[0];
      for (var id in peerConnections) {
        var sender = peerConnections[id].getSenders().find(function (s) {
          return s.track && s.track.kind === 'video';
        });
        if (sender) sender.replaceTrack(screenTrack);
      }
      // Update local tile
      var localTile = document.getElementById('tile-' + PEER_ID);
      if (localTile) localTile.querySelector('video').srcObject = stream;
      screenTrack.onended = stopScreenShare;
    }).catch(function (err) {
      console.error('Screen share failed:', err);
    });
  }

  function stopScreenShare() {
    if (screenStream) {
      screenStream.getTracks().forEach(function (t) { t.stop(); });
      screenStream = null;
    }
    screenSharing = false;
    if (btnScreen) btnScreen.classList.remove('btn-on');
    // Restore camera track
    if (localStream) {
      var camTrack = localStream.getVideoTracks()[0];
      if (camTrack) {
        for (var id in peerConnections) {
          var sender = peerConnections[id].getSenders().find(function (s) {
            return s.track && s.track.kind === 'video';
          });
          if (sender) sender.replaceTrack(camTrack);
        }
      }
      var localTile = document.getElementById('tile-' + PEER_ID);
      if (localTile) localTile.querySelector('video').srcObject = localStream;
    }
  }

  function leaveCall() {
    if (localStream) localStream.getTracks().forEach(function (t) { t.stop(); });
    if (screenStream) screenStream.getTracks().forEach(function (t) { t.stop(); });
    for (var id in peerConnections) peerConnections[id].close();
    if (ws) ws.close();
    window.location.href = window.VIDEO_CONFIG.leaveUrl || '/';
  }

  // ---- Leader: mute all ----
  window.addEventListener('video-mute-all', function () {
    sendSignal({ type: 'mute_all' });
  });

  // ---- Start ----
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();