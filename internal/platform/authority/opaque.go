package authority

func opaqueMarshal() ([]byte, error) { return nil, ErrOpaqueAuthority }
func opaqueUnmarshal([]byte) error   { return ErrOpaqueAuthority }

func (VerifiedPrincipal) MarshalJSON() ([]byte, error)     { return opaqueMarshal() }
func (*VerifiedPrincipal) UnmarshalJSON(data []byte) error { return opaqueUnmarshal(data) }
func (VerifiedPrincipal) MarshalText() ([]byte, error)     { return opaqueMarshal() }
func (*VerifiedPrincipal) UnmarshalText(data []byte) error { return opaqueUnmarshal(data) }

func (OperatorAuthority) MarshalJSON() ([]byte, error)     { return opaqueMarshal() }
func (*OperatorAuthority) UnmarshalJSON(data []byte) error { return opaqueUnmarshal(data) }
func (OperatorAuthority) MarshalText() ([]byte, error)     { return opaqueMarshal() }
func (*OperatorAuthority) UnmarshalText(data []byte) error { return opaqueUnmarshal(data) }

func (ExecutionAuthority) MarshalJSON() ([]byte, error)     { return opaqueMarshal() }
func (*ExecutionAuthority) UnmarshalJSON(data []byte) error { return opaqueUnmarshal(data) }
func (ExecutionAuthority) MarshalText() ([]byte, error)     { return opaqueMarshal() }
func (*ExecutionAuthority) UnmarshalText(data []byte) error { return opaqueUnmarshal(data) }

func (SessionAuthority) MarshalJSON() ([]byte, error)     { return opaqueMarshal() }
func (*SessionAuthority) UnmarshalJSON(data []byte) error { return opaqueUnmarshal(data) }
func (SessionAuthority) MarshalText() ([]byte, error)     { return opaqueMarshal() }
func (*SessionAuthority) UnmarshalText(data []byte) error { return opaqueUnmarshal(data) }

func (WebhookAuthority) MarshalJSON() ([]byte, error)     { return opaqueMarshal() }
func (*WebhookAuthority) UnmarshalJSON(data []byte) error { return opaqueUnmarshal(data) }
func (WebhookAuthority) MarshalText() ([]byte, error)     { return opaqueMarshal() }
func (*WebhookAuthority) UnmarshalText(data []byte) error { return opaqueUnmarshal(data) }

func (SystemAuthority) MarshalJSON() ([]byte, error)     { return opaqueMarshal() }
func (*SystemAuthority) UnmarshalJSON(data []byte) error { return opaqueUnmarshal(data) }
func (SystemAuthority) MarshalText() ([]byte, error)     { return opaqueMarshal() }
func (*SystemAuthority) UnmarshalText(data []byte) error { return opaqueUnmarshal(data) }
