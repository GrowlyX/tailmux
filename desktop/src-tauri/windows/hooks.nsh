; Uninstalling the app takes the tailmux service with it; otherwise it
; keeps running TUN mode (and its DNS rules) with no app left to stop it.
; Updates reinstall over the top and leave the service alone.
!macro NSIS_HOOK_PREUNINSTALL
  ${If} $UpdateMode <> 1
    nsExec::Exec 'sc.exe query tailmux'
    Pop $0
    ${If} $0 = 0
      DetailPrint "Removing the tailmux service"
      ; This installer runs per user; removing a service needs Administrator.
      ExecShellWait "runas" "$INSTDIR\tailmux.exe" "service uninstall" SW_HIDE
    ${EndIf}
  ${EndIf}
!macroend
