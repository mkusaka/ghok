#include "textflag.h"

TEXT ·sigactionTrampoline(SB),NOSPLIT,$0-0
	JMP process_sigaction(SB)
TEXT ·sigprocmaskTrampoline(SB),NOSPLIT,$0-0
	JMP process_sigprocmask(SB)
TEXT ·raiseTrampoline(SB),NOSPLIT,$0-0
	JMP process_raise(SB)
TEXT ·pidinfoTrampoline(SB),NOSPLIT,$0-0
	JMP process_pidinfo(SB)

TEXT ·sigactionAddress(SB),NOSPLIT,$0-8
	MOVQ $·sigactionTrampoline(SB), AX
	MOVQ AX, ret+0(FP)
	RET
TEXT ·sigprocmaskAddress(SB),NOSPLIT,$0-8
	MOVQ $·sigprocmaskTrampoline(SB), AX
	MOVQ AX, ret+0(FP)
	RET
TEXT ·raiseAddress(SB),NOSPLIT,$0-8
	MOVQ $·raiseTrampoline(SB), AX
	MOVQ AX, ret+0(FP)
	RET
TEXT ·pidinfoAddress(SB),NOSPLIT,$0-8
	MOVQ $·pidinfoTrampoline(SB), AX
	MOVQ AX, ret+0(FP)
	RET
