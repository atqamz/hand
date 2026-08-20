Name: hand
Version: %{_hand_version}
Release: 1%{?dist}
Summary: Manage a fleet of coding agents
License: MIT
URL: https://github.com/atqamz/hand
Source0: hand

%description
Secondhand's hand CLI - one worker per task, its own worktree and herdr pane.

%install
install -Dm755 %{SOURCE0} %{buildroot}%{_bindir}/hand

%files
%{_bindir}/hand
