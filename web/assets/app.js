document.addEventListener('DOMContentLoaded', function () {
  if (window.lucide) { lucide.createIcons(); }

  var root = document.documentElement;
  var liveUpdatesStorageKey = 'opensandbox-live-updates';
  var sandboxFilterStorageKey = 'opensandbox-state-filter';
  var snapshotFilterStorageKey = 'opensandbox-snapshot-state-filter';
  var detailsPaneStorageKey = 'opensandbox-details-pane';
  var themeStorageKey = 'opensandbox-theme';
  var themeSwitch = document.getElementById('theme-switch');
  var settingsMenuToggle = document.getElementById('settings-menu-toggle');
  var settingsMenu = settingsMenuToggle.closest('.nav-menu');
  var createSandboxModal = document.getElementById('create-sandbox-modal');
  var createSandboxForm = document.getElementById('create-sandbox-form');
  var createSandboxError = document.getElementById('create-sandbox-error');
  var createSnapshotModal = document.getElementById('create-snapshot-modal');
  var createSnapshotForm = document.getElementById('create-snapshot-form');
  var createSnapshotError = document.getElementById('create-snapshot-error');
  var imageInput = createSandboxForm.elements.image;
  var resourcePresetInput = createSandboxForm.elements.resourcePreset;
  var resourcePresetLabel = document.getElementById('resource-preset-label');
  var confirmationModal = document.getElementById('confirmation-modal');
  var confirmationTitle = document.getElementById('confirmation-modal-title');
  var confirmationMessage = document.getElementById('confirmation-modal-message');
  var confirmationSubmit = document.getElementById('confirmation-modal-submit');

  function applyTheme(theme, persist) {
    var selectedTheme = theme === 'dim' ? 'dim' : 'corporate';
    root.setAttribute('data-theme', selectedTheme);
    if (persist) { localStorage.setItem(themeStorageKey, selectedTheme); }

    themeSwitch.setAttribute('aria-checked', selectedTheme === 'dim' ? 'true' : 'false');
  }

  themeSwitch.addEventListener('click', function () {
    applyTheme(root.getAttribute('data-theme') === 'dim' ? 'corporate' : 'dim', true);
  });

  var colorScheme = window.matchMedia('(prefers-color-scheme: dark)');
  colorScheme.addEventListener('change', function (event) {
    if (!localStorage.getItem(themeStorageKey)) {
      applyTheme(event.matches ? 'dim' : 'corporate', false);
    }
  });
  applyTheme(root.getAttribute('data-theme'), false);

  function dismissModal(modal, returnValue) {
    if (!modal.open || modal.classList.contains('is-closing')) { return; }
    modal.returnValue = returnValue || 'cancel';
    modal.classList.add('is-closing');

    var closeTimer;
    function finishClose() {
      modal.removeEventListener('animationend', handleAnimationEnd);
      clearTimeout(closeTimer);
      modal.classList.remove('is-closing');
      if (modal.open) { modal.close(modal.returnValue); }
    }
    function handleAnimationEnd(event) {
      if (event.target === modal && event.animationName === 'modal-exit') { finishClose(); }
    }

    modal.addEventListener('animationend', handleAnimationEnd);
    closeTimer = setTimeout(finishClose, 180);
  }

  window.osbModal = {
    confirm: function (options) {
      confirmationTitle.textContent = options.title || 'Confirm action';
      confirmationMessage.textContent = options.message || 'Are you sure?';
      confirmationSubmit.textContent = options.confirmLabel || 'Confirm';
      confirmationSubmit.setAttribute('data-variant', options.variant || 'primary');
      confirmationModal.classList.remove('is-closing');
      confirmationModal.returnValue = '';
      confirmationModal.showModal();
      requestAnimationFrame(function () {
        confirmationModal.querySelector('.app-modal-footer [value="cancel"]').focus();
      });

      return new Promise(function (resolve) {
        confirmationModal.addEventListener('close', function () {
          resolve(confirmationModal.returnValue === 'confirm');
        }, { once: true });
      });
    },
    dismiss: function (returnValue) { dismissModal(confirmationModal, returnValue); }
  };

  confirmationModal.addEventListener('cancel', function (event) {
    event.preventDefault();
    dismissModal(confirmationModal, 'cancel');
  });

  confirmationModal.addEventListener('click', function (event) {
    var closeControl = event.target.closest('[data-modal-close]');
    if (closeControl) {
      dismissModal(confirmationModal, closeControl.value);
      return;
    }
    if (event.target === confirmationModal) { dismissModal(confirmationModal, 'cancel'); }
  });

  var presetMenus = [];

  function setupPresetMenu(toggleID, menuID, getValue, selectValue, focusTarget) {
    var toggle = document.getElementById(toggleID);
    var menu = document.getElementById(menuID);
    var control = toggle.closest('.preset-input-control');

    function setOpen(open, focusMenu) {
      toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
      menu.hidden = !open;
      menu.querySelectorAll('[data-preset-value]').forEach(function (option) {
        option.setAttribute('data-selected', option.dataset.presetValue === getValue() ? 'true' : 'false');
      });
      if (open && focusMenu) {
        requestAnimationFrame(function () {
          var selected = menu.querySelector('[data-selected="true"]');
          (selected || menu.querySelector('[data-preset-value]')).focus();
        });
      }
    }

    var controller = { close: function () { setOpen(false, false); } };
    presetMenus.push(controller);

    toggle.addEventListener('click', function () {
      var open = toggle.getAttribute('aria-expanded') !== 'true';
      presetMenus.forEach(function (presetMenu) { presetMenu.close(); });
      setOpen(open, true);
    });

    menu.addEventListener('click', function (event) {
      var option = event.target.closest('[data-preset-value]');
      if (!option) { return; }
      selectValue(option.dataset.presetValue, option.dataset.presetLabel || option.dataset.presetValue);
      setOpen(false, false);
      focusTarget.focus();
    });

    control.addEventListener('keydown', function (event) {
      if (event.key !== 'Escape' || menu.hidden) { return; }
      event.preventDefault();
      event.stopPropagation();
      setOpen(false, false);
      toggle.focus();
    });
  }

  setupPresetMenu(
    'image-presets-toggle',
    'image-presets-menu',
    function () { return imageInput.value; },
    function (value) { imageInput.value = value; },
    imageInput
  );
  setupPresetMenu(
    'resource-presets-toggle',
    'resource-presets-menu',
    function () { return resourcePresetInput.value; },
    function (value, label) {
      resourcePresetInput.value = value;
      resourcePresetLabel.value = label;
    },
    document.getElementById('resource-presets-toggle')
  );

  function closePresetMenus() {
    presetMenus.forEach(function (presetMenu) { presetMenu.close(); });
  }

  document.addEventListener('click', function (event) {
    if (!event.target.closest('.preset-input-control')) { closePresetMenus(); }
  });

  function setCreateSandboxMode(snapshotID, snapshotName) {
    var restoring = Boolean(snapshotID);
    createSandboxForm.elements.snapshotId.value = snapshotID || '';
    createSandboxForm.elements.snapshotName.value = snapshotName || snapshotID || '';
    createSandboxForm.setAttribute('hx-target', restoring ? '#deploy-snapshot-result' : '#dashboard-content');
    createSandboxForm.setAttribute('hx-swap', restoring ? 'innerHTML' : 'outerHTML');
    createSandboxForm.elements.image.disabled = restoring;
    createSandboxForm.elements.image.required = !restoring;
    createSandboxForm.querySelector('[data-sandbox-image-field]').hidden = restoring;
    createSandboxForm.querySelector('[data-snapshot-source-field]').hidden = !restoring;
    var snapshotSource = document.getElementById('sandbox-snapshot-source');
    snapshotSource.textContent = restoring ? (snapshotName || snapshotID) : '';
    snapshotSource.title = restoring ? snapshotID : '';
    document.getElementById('create-sandbox-modal-title').textContent = restoring ? 'Deploy snapshot' : 'Create sandbox';
    createSandboxForm.querySelector('.create-button-label').textContent = restoring ? 'Deploy' : 'Create';
    createSandboxForm.querySelector('[data-create-loading-label]').textContent = restoring ? 'Deploying' : 'Creating';
    var modalIcon = document.querySelector('[data-create-sandbox-icon]');
    modalIcon.removeAttribute('data-state');
    modalIcon.removeAttribute('hx-swap-oob');
    modalIcon.innerHTML = '<i data-lucide="' + (restoring ? 'archive-restore' : 'box') + '"></i>';
    if (window.lucide) { lucide.createIcons(); }
  }

  function openCreateSandboxModal(snapshotID, snapshotName) {
    createSandboxError.hidden = true;
    createSandboxError.textContent = '';
    document.getElementById('deploy-snapshot-result').innerHTML = '';
    createSandboxModal.classList.remove('is-closing');
    createSandboxModal.returnValue = '';
    closePresetMenus();
    setCreateSandboxMode(snapshotID, snapshotName);
    createSandboxModal.showModal();
    requestAnimationFrame(function () {
      (snapshotID ? document.getElementById('resource-presets-toggle') : createSandboxForm.elements.image).focus();
    });
  }

  document.body.addEventListener('click', function (event) {
    var restore = event.target.closest('[data-restore-snapshot]');
    if (restore) {
      openCreateSandboxModal(restore.dataset.snapshotId, restore.dataset.snapshotName);
      return;
    }
    if (event.target.closest('[data-open-create-modal]')) { openCreateSandboxModal('', ''); }
  });

  createSandboxModal.addEventListener('cancel', function (event) {
    event.preventDefault();
    dismissModal(createSandboxModal, 'cancel');
  });

  createSandboxModal.addEventListener('click', function (event) {
    if (event.target.closest('[data-create-modal-close]') || event.target === createSandboxModal) {
      dismissModal(createSandboxModal, 'cancel');
    }
  });

  createSandboxModal.addEventListener('close', function () {
    createSandboxForm.reset();
    document.getElementById('deploy-snapshot-result').innerHTML = '';
    setCreateSandboxMode('', '');
    closePresetMenus();
    createSandboxError.hidden = true;
    createSandboxError.textContent = '';
  });

  createSandboxForm.addEventListener('htmx:beforeRequest', function () {
    createSandboxForm.setAttribute('aria-busy', 'true');
    createSandboxError.hidden = true;
    createSandboxError.textContent = '';
  });

  createSandboxForm.addEventListener('htmx:afterRequest', function () {
    createSandboxForm.setAttribute('aria-busy', 'false');
  });

  createSandboxForm.addEventListener('htmx:responseError', function (event) {
    createSandboxError.textContent = event.detail.xhr.responseText.trim() || 'Unable to create sandbox.';
    createSandboxError.hidden = false;
  });

  document.body.addEventListener('sandboxCreateAccepted', function () {
    dismissModal(createSandboxModal, 'accepted');
  });

  document.body.addEventListener('sandboxDeploymentStarted', function () {
    window.setTimeout(window.refreshDashboard, 500);
  });

  function openCreateSnapshotModal(sandboxID) {
    createSnapshotForm.reset();
    createSnapshotForm.elements.sandboxID.value = sandboxID;
    var sourceSandbox = document.getElementById('snapshot-source-sandbox');
    sourceSandbox.textContent = sandboxID;
    sourceSandbox.title = sandboxID;
    createSnapshotError.hidden = true;
    createSnapshotError.textContent = '';
    document.getElementById('create-snapshot-result').innerHTML = '';
    var modalIcon = document.getElementById('create-snapshot-modal-icon');
    modalIcon.removeAttribute('data-state');
    modalIcon.removeAttribute('hx-swap-oob');
    modalIcon.innerHTML = '<i data-lucide="layers-3"></i>';
    if (window.lucide) { lucide.createIcons(); }
    createSnapshotModal.classList.remove('is-closing');
    createSnapshotModal.returnValue = '';
    closeSandboxActionsMenu();
    createSnapshotModal.showModal();
    requestAnimationFrame(function () { createSnapshotForm.elements.name.focus(); });
  }

  document.body.addEventListener('click', function (event) {
    var action = event.target.closest('[data-open-snapshot-modal]');
    if (action) { openCreateSnapshotModal(action.dataset.sandboxId); }
  });

  createSnapshotModal.addEventListener('cancel', function (event) {
    event.preventDefault();
    dismissModal(createSnapshotModal, 'cancel');
  });

  createSnapshotModal.addEventListener('click', function (event) {
    if (event.target.closest('[data-snapshot-modal-close]') || event.target === createSnapshotModal) {
      dismissModal(createSnapshotModal, 'cancel');
    }
  });

  createSnapshotModal.addEventListener('close', function () {
    createSnapshotForm.reset();
    document.getElementById('create-snapshot-result').innerHTML = '';
    createSnapshotError.hidden = true;
    createSnapshotError.textContent = '';
  });

  createSnapshotForm.addEventListener('htmx:beforeRequest', function () {
    createSnapshotForm.setAttribute('aria-busy', 'true');
    createSnapshotError.hidden = true;
    createSnapshotError.textContent = '';
  });

  createSnapshotForm.addEventListener('htmx:afterRequest', function () {
    createSnapshotForm.setAttribute('aria-busy', 'false');
  });

  createSnapshotForm.addEventListener('htmx:responseError', function (event) {
    createSnapshotError.textContent = event.detail.xhr.responseText.trim() || 'Unable to create snapshot.';
    createSnapshotError.hidden = false;
  });

  document.body.addEventListener('snapshotCreated', function () {
    window.setTimeout(window.refreshDashboard, 500);
  });

  document.body.addEventListener('htmx:afterRequest', function (event) {
    if (!event.detail.successful) { return; }
    if (event.detail.elt.closest('[data-view-snapshot]')) {
      dismissModal(createSnapshotModal, 'view-snapshot');
    }
    if (event.detail.elt.closest('[data-view-sandbox]')) {
      dismissModal(createSandboxModal, 'view-sandbox');
    }
  });

  document.body.addEventListener('htmx:beforeRequest', function (event) {
    var action = event.detail.elt.closest('[data-sandbox-lifecycle-action]');
    if (!action) { return; }
    window.osbSandboxInfoPanel = 'details';
    window.osbLifecycleActionActive = true;
    window.osbTerminalActive = false;
    window.osbTerminalError = null;
    if (window.osbTerminalSocket) {
      window.osbTerminalSocket.close(1000, 'Sandbox state is changing');
      window.osbTerminalSocket = null;
    }
  });

  document.body.addEventListener('sandboxStateChanged', function () {
    window.osbLifecycleActionActive = false;
    window.setTimeout(window.refreshDashboard, 1500);
  });

  document.body.addEventListener('htmx:confirm', function (event) {
    if (!event.detail.question) { return; }
    event.preventDefault();
    var action = event.detail.elt;
    window.osbModal.confirm({
      title: action.dataset.confirmTitle,
      message: event.detail.question,
      confirmLabel: action.dataset.confirmLabel,
      variant: action.dataset.confirmVariant
    }).then(function (confirmed) {
      if (confirmed) { event.detail.issueRequest(true); }
    });
  });

  window.restoreSandboxTerminalError = function () {
    var error = window.osbTerminalError;
    var container = document.getElementById('sandbox-terminal');
    var overlay = document.querySelector('[data-terminal-overlay]');
    if (!error || !container || !overlay || error.sandboxID !== container.dataset.sandboxId) { return; }

    overlay.hidden = false;
    var title = overlay.querySelector('[data-terminal-overlay-title]');
    var message = overlay.querySelector('[data-terminal-overlay-message]');
    var button = overlay.querySelector('[data-terminal-connect]');
    if (title) { title.textContent = 'Terminal unavailable'; }
    if (message) {
      message.textContent = error.message;
      message.hidden = false;
    }
    if (button) {
      button.textContent = 'Retry';
      button.disabled = false;
    }
  };

  window.initializeSandboxTerminal = async function (connect) {
    var container = document.getElementById('sandbox-terminal');
    if (!container) {
      if (window.osbTerminalInstance) {
        window.osbTerminalInstance.dispose();
        window.osbTerminalInstance = null;
      }
      if (window.osbTerminalResizeObserver) {
        window.osbTerminalResizeObserver.disconnect();
        window.osbTerminalResizeObserver = null;
      }
      if (window.osbTerminalSocket) {
        window.osbTerminalSocket.close();
        window.osbTerminalSocket = null;
      }
      window.osbTerminalActive = false;
      return;
    }
    var sameTerminal = window.osbTerminalElement === container;
    if (connect !== true) {
      if (!sameTerminal) {
        if (window.osbTerminalInstance) { window.osbTerminalInstance.dispose(); }
        if (window.osbTerminalResizeObserver) { window.osbTerminalResizeObserver.disconnect(); }
        if (window.osbTerminalSocket) { window.osbTerminalSocket.close(); }
        window.osbTerminalInstance = null;
        window.osbTerminalResizeObserver = null;
        window.osbTerminalSocket = null;
        window.osbTerminalElement = container;
        window.osbTerminalActive = false;
        return;
      }
      window.osbTerminalActive = Boolean(
        window.osbTerminalSocket && window.osbTerminalSocket.readyState <= WebSocket.OPEN
      );
      return;
    }
    if (container.dataset.terminalEnabled !== 'true') { return; }
    var sandboxID = container.dataset.sandboxId;
    if (connect === true && window.osbTerminalError?.sandboxID === sandboxID) {
      window.osbTerminalError = null;
    }
    if (sameTerminal && window.osbTerminalSocket && window.osbTerminalSocket.readyState <= WebSocket.OPEN) { return; }
    if (window.osbTerminalInstance) { window.osbTerminalInstance.dispose(); }
    if (window.osbTerminalResizeObserver) { window.osbTerminalResizeObserver.disconnect(); }
    if (window.osbTerminalSocket) { window.osbTerminalSocket.close(); }

    window.osbTerminalActive = true;
    window.osbTerminalElement = container;
    var overlay = document.querySelector('[data-terminal-overlay]');
    var overlayTitle = overlay && overlay.querySelector('[data-terminal-overlay-title]');
    var overlayMessage = overlay && overlay.querySelector('[data-terminal-overlay-message]');
    var connectButton = overlay && overlay.querySelector('[data-terminal-connect]');

    function showOverlay(title, message, buttonLabel, buttonDisabled) {
      if (!overlay) { return; }
      overlay.hidden = false;
      if (overlayTitle) { overlayTitle.textContent = title; }
      if (overlayMessage) {
        overlayMessage.textContent = message || '';
        overlayMessage.hidden = !message;
      }
      if (connectButton) {
        connectButton.textContent = buttonLabel;
        connectButton.disabled = buttonDisabled;
      }
    }

    showOverlay('Connecting terminal', 'Opening an interactive PTY session through OpenSandbox.', 'Connecting', true);
    try {
      var ghostty = await import((window.osbBasePath || '') + '/assets/third-party/ghostty-web/ghostty-web.js');
      await ghostty.init();
      var terminal = new ghostty.Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
        theme: { background: '#111318', foreground: '#d8dee9', cursor: '#88c0d0' }
      });
      var fitAddon = new ghostty.FitAddon();
      terminal.loadAddon(fitAddon);
      container.innerHTML = '';
      terminal.open(container);
      fitAddon.fit();
      window.osbTerminalInstance = terminal;
      window.osbTerminalResizeObserver = new ResizeObserver(function () { fitAddon.fit(); });
      window.osbTerminalResizeObserver.observe(container);

      var websocketScheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      var websocketURL = websocketScheme + '//' + window.location.host + (window.osbBasePath || '') + '/dashboard/sandboxes/' + encodeURIComponent(sandboxID) + '/terminal/pty';
      var socket = new WebSocket(websocketURL);
      socket.binaryType = 'arraybuffer';
      window.osbTerminalSocket = socket;
      var encoder = new TextEncoder();
      var terminalFailure = false;
      var connectionTimer = window.setTimeout(function () {
        failTerminal('OpenSandbox did not finish starting the terminal.');
        socket.close();
      }, 15 * 1000);

      function failTerminal(message) {
        var failureMessage = message || 'The terminal could not be started.';
        terminalFailure = true;
        window.clearTimeout(connectionTimer);
        window.osbTerminalActive = false;
        window.osbTerminalError = { sandboxID: sandboxID, message: failureMessage };
        showOverlay('Terminal unavailable', failureMessage, 'Retry', false);
      }

      function sendChannel(channel, payload) {
        if (socket.readyState !== WebSocket.OPEN) { return; }
        var data = typeof payload === 'string' ? encoder.encode(payload) : payload;
        var message = new Uint8Array(data.length + 1);
        message[0] = channel;
        message.set(data, 1);
        socket.send(message);
      }

      function sendTerminalSize(size) {
        if (socket.readyState !== WebSocket.OPEN) { return; }
        socket.send(JSON.stringify({ type: 'resize', cols: size.cols, rows: size.rows }));
      }

      socket.addEventListener('open', function () {
        sendTerminalSize({ cols: terminal.cols, rows: terminal.rows });
      });
      socket.addEventListener('message', function (event) {
        if (typeof event.data === 'string') {
          try {
            var message = JSON.parse(event.data);
            if (message.type === 'connected') {
              window.clearTimeout(connectionTimer);
              window.osbTerminalError = null;
              if (overlay) { overlay.hidden = true; }
              terminal.focus();
            } else if (message.type === 'error') {
              failTerminal(message.message || message.error);
            }
          } catch (_) {}
          return;
        }
        var message = new Uint8Array(event.data);
        if (!message.length) { return; }
        if (message[0] === 1 || message[0] === 2) {
          terminal.write(message.slice(1));
        } else if (message[0] === 3) {
          terminal.write('\r\n\x1b[31m' + new TextDecoder().decode(message.slice(1)) + '\x1b[0m\r\n');
        }
      });
      socket.addEventListener('close', function () {
        window.clearTimeout(connectionTimer);
        window.osbTerminalActive = false;
        if (!terminalFailure) {
          showOverlay('Terminal disconnected', 'The PTY connection has closed.', 'Reconnect', false);
        }
      });
      socket.addEventListener('error', function () {
        if (!terminalFailure) {
          failTerminal('The PTY connection could not be opened.');
        }
      });

      terminal.onData(function (data) { sendChannel(0, encoder.encode(data)); });
      terminal.onResize(function (size) { sendTerminalSize(size); });
    } catch (error) {
      window.osbTerminalActive = false;
      window.osbTerminalError = { sandboxID: sandboxID, message: error.message };
      showOverlay('Terminal unavailable', error.message, 'Retry', false);
    }
  };

  document.body.addEventListener('click', function (event) {
    if (event.target.closest('[data-terminal-connect]')) {
      window.initializeSandboxTerminal(true);
    }
  });

  function closeSandboxActionsMenu() {
    var toggle = document.querySelector('[data-sandbox-actions-toggle]');
    var menu = document.querySelector('.sandbox-actions-popover');
    if (toggle) { toggle.setAttribute('aria-expanded', 'false'); }
    if (menu) { menu.hidden = true; }
  }

  document.body.addEventListener('click', function (event) {
    var toggle = event.target.closest('[data-sandbox-actions-toggle]');
    if (toggle) {
      var menu = toggle.parentElement.querySelector('.sandbox-actions-popover');
      var open = toggle.getAttribute('aria-expanded') !== 'true';
      closeSandboxActionsMenu();
      toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
      menu.hidden = !open;
      return;
    }
    if (!event.target.closest('.sandbox-actions-menu')) { closeSandboxActionsMenu(); }
  });

  document.body.addEventListener('keydown', function (event) {
    if (event.key === 'Escape' && event.target.closest('.sandbox-actions-menu')) {
      closeSandboxActionsMenu();
    }
    if (event.key === 'Escape' && event.target.closest('.sandbox-info-menu')) {
      closeSandboxInfoMenu();
    }
  });

  function closeSandboxInfoMenu() {
    document.querySelectorAll('[data-sandbox-info-toggle]').forEach(function (toggle) {
      toggle.setAttribute('aria-expanded', 'false');
    });
    document.querySelectorAll('.sandbox-info-popover').forEach(function (menu) {
      menu.hidden = true;
    });
  }

  window.applySandboxInfoPanel = function (animate) {
    var options = Array.from(document.querySelectorAll('[data-sandbox-info-panel]'));
    if (!options.length) { return; }
    var selected = window.osbSandboxInfoPanel || 'details';
    if (!options.some(function (option) { return option.dataset.sandboxInfoPanel === selected; })) {
      selected = 'details';
    }
    window.osbSandboxInfoPanel = selected;
    options.forEach(function (option) {
      option.setAttribute('aria-checked', option.dataset.sandboxInfoPanel === selected ? 'true' : 'false');
    });
    document.querySelectorAll('[data-sandbox-info-content]').forEach(function (content) {
      var active = content.dataset.sandboxInfoContent === selected;
      content.classList.remove('is-switching');
      content.hidden = !active;
      if (active && animate) {
        void content.offsetWidth;
        content.classList.add('is-switching');
        content.addEventListener('animationend', function () {
          content.classList.remove('is-switching');
        }, { once: true });
      }
    });
    var selectedOption = options.find(function (option) { return option.dataset.sandboxInfoPanel === selected; });
    if (selectedOption) {
      var selectedLabel = selectedOption.textContent.trim();
      var selectedIcon = selectedOption.dataset.sandboxInfoPanelIcon || 'info';
      document.querySelectorAll('[data-sandbox-info-label]').forEach(function (label) {
        label.textContent = selectedLabel;
      });
      document.querySelectorAll('[data-sandbox-info-icon]').forEach(function (icon) {
        icon.innerHTML = '<i data-lucide="' + selectedIcon + '"></i>';
      });
      if (window.lucide) { lucide.createIcons(); }
      if (selected === 'stats' && window.htmx) {
        var stats = document.getElementById('sandbox-live-stats');
        if (stats && (animate || stats.dataset.statsLoaded !== 'true')) {
          htmx.trigger(stats, 'statsRefresh');
        }
      } else if (selected === 'events' && window.htmx) {
        var events = document.getElementById('sandbox-events');
        if (events && (animate || events.dataset.eventsLoaded !== 'true')) {
          htmx.trigger(events, 'eventsRefresh');
        }
      }
    }
  };

  document.body.addEventListener('click', function (event) {
    var toggle = event.target.closest('[data-sandbox-info-toggle]');
    if (toggle) {
      var menu = toggle.parentElement.querySelector('.sandbox-info-popover');
      var open = toggle.getAttribute('aria-expanded') !== 'true';
      closeSandboxInfoMenu();
      toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
      menu.hidden = !open;
      return;
    }
    var option = event.target.closest('[data-sandbox-info-panel]');
    if (option) {
      window.osbSandboxInfoPanel = option.dataset.sandboxInfoPanel;
      window.applySandboxInfoPanel(true);
      closeSandboxInfoMenu();
      return;
    }
    if (!event.target.closest('.sandbox-info-menu')) { closeSandboxInfoMenu(); }
  });

  window.updatePageActions = function () {
    var content = document.getElementById('dashboard-content');
    var toggle = document.getElementById('details-pane-toggle');
    if (!content || content.dataset.page !== 'detail') { return; }

    var collapsed = localStorage.getItem(detailsPaneStorageKey) === 'collapsed';
    content.setAttribute('data-details-collapsed', collapsed ? 'true' : 'false');
    if (toggle) {
      toggle.setAttribute('aria-pressed', collapsed ? 'true' : 'false');
      toggle.setAttribute('aria-label', collapsed ? 'Show details pane' : 'Hide details pane');
      toggle.setAttribute('title', collapsed ? 'Show details pane' : 'Hide details pane');
      toggle.innerHTML = '<i data-lucide="' + (collapsed ? 'panel-right-open' : 'panel-right-close') + '"></i>';
      if (window.lucide) { lucide.createIcons(); }
    }
  };

  document.body.addEventListener('click', function (event) {
    if (!event.target.closest('#details-pane-toggle')) { return; }
    var collapsed = localStorage.getItem(detailsPaneStorageKey) === 'collapsed';
    localStorage.setItem(detailsPaneStorageKey, collapsed ? 'expanded' : 'collapsed');
    window.updatePageActions();
  });

  window.localizeSandboxTimes = function () {
    var absoluteFormatter = new Intl.DateTimeFormat(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
      second: '2-digit',
      timeZoneName: 'short'
    });
    var relativeFormatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
    var now = Date.now();
    var relativeThreshold = 3 * 60 * 60 * 1000;

    document.querySelectorAll('time[data-created-time]').forEach(function (element) {
      var value = new Date(element.getAttribute('datetime'));
      if (Number.isNaN(value.getTime())) { return; }

      var difference = value.getTime() - now;
      var prefix = element.hasAttribute('data-no-prefix') ? '' : 'created ';
      if (Math.abs(difference) <= relativeThreshold) {
        var unit = 'second';
        var divisor = 1000;
        if (Math.abs(difference) >= 60 * 60 * 1000) {
          unit = 'hour';
          divisor = 60 * 60 * 1000;
        } else if (Math.abs(difference) >= 60 * 1000) {
          unit = 'minute';
          divisor = 60 * 1000;
        }
        element.textContent = prefix + relativeFormatter.format(Math.round(difference / divisor), unit);
      } else {
        element.textContent = prefix + absoluteFormatter.format(value);
      }
      element.title = value.toISOString();
    });
  };

  window.initializeSandboxDeleteButtons = function () {
    document.querySelectorAll('.sandbox-delete-button:not([data-click-isolated])').forEach(function (button) {
      button.setAttribute('data-click-isolated', 'true');
      button.addEventListener('click', function (event) { event.stopPropagation(); });
    });
  };

  window.refreshDashboard = function () {
    if (document.visibilityState !== 'visible' || window.osbLiveUpdatesEnabled === false || window.osbLifecycleActionActive === true || !window.htmx) { return; }
    var content = document.getElementById('dashboard-content');
    if (content && content.dataset.page === 'detail' && window.osbSandboxInfoPanel === 'stats') {
      var stats = document.getElementById('sandbox-live-stats');
      if (stats) { htmx.trigger(stats, 'statsRefresh'); }
      return;
    }
    if (content && content.dataset.page === 'detail' && window.osbSandboxInfoPanel === 'events') {
      var events = document.getElementById('sandbox-events');
      if (events) { htmx.trigger(events, 'eventsRefresh'); }
      return;
    }
    if (window.osbTerminalActive === true) { return; }
    if (content) { htmx.trigger(content, 'dashboardRefresh'); }
  };

  window.applySandboxFilter = function () {
    var pills = Array.from(document.querySelectorAll('[data-state-filter]'));
    if (!pills.length) { return; }

    var selected = window.osbSandboxFilter || 'all';
    if (!pills.some(function (pill) { return pill.dataset.stateFilter === selected; })) {
      selected = 'all';
      window.osbSandboxFilter = selected;
      localStorage.setItem(sandboxFilterStorageKey, selected);
    }

    pills.forEach(function (pill) {
      var active = pill.dataset.stateFilter === selected;
      pill.setAttribute('data-active', active ? 'true' : 'false');
      pill.setAttribute('aria-pressed', active ? 'true' : 'false');
    });

    var groups = Array.from(document.querySelectorAll('[data-sandbox-state]'));
    var visibleGroups = 0;
    groups.forEach(function (group) {
      var visible = selected === 'all' || group.dataset.sandboxState === selected;
      group.hidden = !visible;
      group.classList.toggle('is-filtered-view', visible && selected !== 'all');
      if (visible) { visibleGroups += 1; }
    });

    var filteredEmpty = document.querySelector('[data-filter-empty]');
    if (filteredEmpty) { filteredEmpty.hidden = visibleGroups !== 0; }
  };

  document.body.addEventListener('click', function (event) {
    var pill = event.target.closest('[data-state-filter]');
    if (!pill) { return; }
    window.osbSandboxFilter = pill.dataset.stateFilter;
    localStorage.setItem(sandboxFilterStorageKey, window.osbSandboxFilter);
    window.applySandboxFilter();
  });

  window.applySnapshotFilter = function () {
    var pills = Array.from(document.querySelectorAll('[data-snapshot-state-filter]'));
    if (!pills.length) { return; }

    var selected = window.osbSnapshotFilter || 'all';
    if (!pills.some(function (pill) { return pill.dataset.snapshotStateFilter === selected; })) {
      selected = 'all';
      window.osbSnapshotFilter = selected;
      localStorage.setItem(snapshotFilterStorageKey, selected);
    }
    pills.forEach(function (pill) {
      var active = pill.dataset.snapshotStateFilter === selected;
      pill.setAttribute('data-active', active ? 'true' : 'false');
      pill.setAttribute('aria-pressed', active ? 'true' : 'false');
    });

    var groups = Array.from(document.querySelectorAll('[data-snapshot-state]'));
    var visibleGroups = 0;
    groups.forEach(function (group) {
      var visible = selected === 'all' || group.dataset.snapshotState === selected;
      group.hidden = !visible;
      group.classList.toggle('is-filtered-view', visible && selected !== 'all');
      if (visible) { visibleGroups += 1; }
    });
    var filteredEmpty = document.querySelector('[data-snapshot-filter-empty]');
    if (filteredEmpty) { filteredEmpty.hidden = visibleGroups !== 0; }
  };

  document.body.addEventListener('click', function (event) {
    var pill = event.target.closest('[data-snapshot-state-filter]');
    if (!pill) { return; }
    window.osbSnapshotFilter = pill.dataset.snapshotStateFilter;
    localStorage.setItem(snapshotFilterStorageKey, window.osbSnapshotFilter);
    window.applySnapshotFilter();
  });

  window.updatePrimaryNavigation = function () {
    var content = document.getElementById('dashboard-content');
    if (!content) { return; }
    var page = content.dataset.page === 'stats' ? 'stats' : (content.dataset.page.indexOf('snapshot') === 0 ? 'snapshots' : 'sandboxes');
    document.querySelectorAll('[data-nav-page]').forEach(function (item) {
      if (item.dataset.navPage === page) {
        item.setAttribute('aria-current', 'page');
      } else {
        item.removeAttribute('aria-current');
      }
    });
  };

  function liveUpdatesEnabled() {
    return window.osbLiveUpdatesEnabled !== false;
  }

  function updateLiveUpdatesToggle() {
    var toggle = document.getElementById('live-updates-toggle');
    if (!toggle) { return; }
    var enabled = liveUpdatesEnabled();
    toggle.setAttribute('data-live-state', enabled ? 'enabled' : 'paused');
    toggle.setAttribute('aria-label', enabled ? 'Live updates on' : 'Live updates off');
    toggle.setAttribute('title', enabled ? 'Live updates on' : 'Live updates off');
    toggle.innerHTML = '<i data-lucide="' + (enabled ? 'refresh-cw' : 'refresh-cw-off') + '"></i>';
    if (window.lucide) { lucide.createIcons(); }
  }

  var liveToggle = document.getElementById('live-updates-toggle');
  if (liveToggle) {
    liveToggle.addEventListener('click', function () {
      window.osbLiveUpdatesEnabled = !liveUpdatesEnabled();
      localStorage.setItem(liveUpdatesStorageKey, liveUpdatesEnabled() ? 'enabled' : 'paused');
      updateLiveUpdatesToggle();
    });
  }
  updateLiveUpdatesToggle();

  function sidebarCollapsed() {
    return root.getAttribute('data-sidebar') === 'collapsed';
  }

  function updateSidebarLabel() {
    var toggle = document.getElementById('sidebar-toggle');
    if (toggle) {
      toggle.setAttribute('aria-label', sidebarCollapsed() ? 'Expand sidebar' : 'Collapse sidebar');
    }
  }

  function setSettingsMenuState(open) {
    settingsMenuToggle.setAttribute('aria-expanded', open ? 'true' : 'false');
  }

  settingsMenu.addEventListener('mouseenter', function () { setSettingsMenuState(true); });
  settingsMenu.addEventListener('mouseleave', function () { setSettingsMenuState(false); });
  settingsMenu.addEventListener('focusin', function () { setSettingsMenuState(true); });
  settingsMenu.addEventListener('focusout', function () {
    requestAnimationFrame(function () {
      setSettingsMenuState(settingsMenu.contains(document.activeElement));
    });
  });
  settingsMenu.addEventListener('keydown', function (event) {
    if (event.key !== 'Escape') { return; }
    settingsMenuToggle.focus();
    settingsMenuToggle.blur();
    setSettingsMenuState(false);
  });

  function setSidebar(state) {
    root.setAttribute('data-sidebar', state);
    localStorage.setItem('opensandbox-sidebar', state);
    updateSidebarLabel();
  }

  var sidebarToggle = document.getElementById('sidebar-toggle');
  if (sidebarToggle) {
    sidebarToggle.addEventListener('click', function () {
      setSidebar(sidebarCollapsed() ? 'expanded' : 'collapsed');
    });
  }

  var workspaceMark = document.getElementById('workspace-mark');
  if (workspaceMark) {
    workspaceMark.addEventListener('click', function () {
      if (sidebarCollapsed()) { setSidebar('expanded'); }
    });
  }

  document.body.addEventListener('click', function (event) {
    var row = event.target.closest('.snapshot-row[role="link"]');
    if (!row || event.target.closest('a, button, input, select, textarea')) { return; }
    var link = row.querySelector('[data-snapshot-row-link]');
    if (link) { link.click(); }
  });

  document.body.addEventListener('keydown', function (event) {
    if (event.key !== 'Enter' && event.key !== ' ') { return; }
    if (event.target.closest('button, a, input, select, textarea')) { return; }
    var action = event.target.closest('[role="button"][data-open-create-modal], [role="link"][hx-get], .snapshot-row[role="link"]');
    if (!action) { return; }
    event.preventDefault();
    action.click();
  });
  updateSidebarLabel();
  window.applySandboxFilter();
  window.applySnapshotFilter();
  window.updatePrimaryNavigation();
  window.localizeSandboxTimes();
  window.updatePageActions();
  window.applySandboxInfoPanel();
  window.initializeSandboxDeleteButtons();
  window.initializeSandboxTerminal();
  window.restoreSandboxTerminalError();
  window.setInterval(window.refreshDashboard, 5 * 1000);
  window.setInterval(window.localizeSandboxTimes, 30 * 1000);
});

document.body.addEventListener('htmx:afterSwap', function () {
  if (window.osbLifecycleActionActive) { window.osbLifecycleActionActive = false; }
  if (window.lucide) { lucide.createIcons(); }
  if (window.applySandboxFilter) { window.applySandboxFilter(); }
  if (window.applySnapshotFilter) { window.applySnapshotFilter(); }
  if (window.updatePrimaryNavigation) { window.updatePrimaryNavigation(); }
  if (window.localizeSandboxTimes) { window.localizeSandboxTimes(); }
  if (window.updatePageActions) { window.updatePageActions(); }
  if (window.applySandboxInfoPanel) { window.applySandboxInfoPanel(); }
  if (window.initializeSandboxDeleteButtons) { window.initializeSandboxDeleteButtons(); }
  if (window.initializeSandboxTerminal) { window.initializeSandboxTerminal(); }
  if (window.restoreSandboxTerminalError) { window.restoreSandboxTerminalError(); }
});
