package api

const (
	ProtoOAApplicationAuthReq = uint32(2100)
	ProtoOAApplicationAuthRes = uint32(2101)
	ProtoOAAccountAuthReq     = uint32(2102)
	ProtoOAAccountAuthRes     = uint32(2103)
	ProtoOATraderReq          = uint32(2104)
	ProtoOATraderRes          = uint32(2105)
	ProtoOANewOrderReq        = uint32(2106)
	ProtoOAAmendPositionSLTPReq = uint32(2110)
	ProtoOAClosePositionReq     = uint32(2111)
	ProtoOAReconcileReq       = uint32(2124)
	ProtoOAReconcileRes       = uint32(2125)
	ProtoOAExecutionEvent     = uint32(2126)
	ProtoOASubscribeSpotsReq  = uint32(2127)
	ProtoOASubscribeSpotsRes  = uint32(2128)
	ProtoOASpotEvent          = uint32(2131)
	ProtoOAErrorRes           = uint32(2142)

	ProtoOAGetAccountListByAccessTokenReq = uint32(2149)
	ProtoOAGetAccountListByAccessTokenRes = uint32(2150)

	ProtoOADealListReq = uint32(2161)
	ProtoOADealListRes = uint32(2162)

	ProtoOASymbolsListReq = uint32(2116)
	ProtoOASymbolsListRes = uint32(2117)

	ProtoOASymbolByIdReq = uint32(2118)
	ProtoOASymbolByIdRes = uint32(2119)


	ProtoOASubscribeLiveTrendbarReq = uint32(2135)
	ProtoOASubscribeLiveTrendbarRes = uint32(2165)
	ProtoOAGetTrendbarsReq          = uint32(2137)
	ProtoOAGetTrendbarsRes          = uint32(2138)
	ProtoOAOrderErrorEvent          = uint32(2132)

	TradeSideBuy  = uint32(1)
	TradeSideSell = uint32(2)

	// ProtoOATrendbarPeriod enum values
	PeriodM1  = uint32(1)
	PeriodM2  = uint32(2)
	PeriodM3  = uint32(3)
	PeriodM4  = uint32(4)
	PeriodM5  = uint32(5)
	PeriodM10 = uint32(6)
	PeriodM15 = uint32(7)
	PeriodM30 = uint32(8)
	PeriodH1  = uint32(9)
	PeriodH4  = uint32(10)
	PeriodD1  = uint32(11)
	PeriodW1  = uint32(12)
	PeriodMN1 = uint32(13)
)

func PeriodToString(period uint32) string {
	switch period {
	case PeriodM1:
		return "M1"
	case PeriodM2:
		return "M2"
	case PeriodM3:
		return "M3"
	case PeriodM4:
		return "M4"
	case PeriodM5:
		return "M5"
	case PeriodM10:
		return "M10"
	case PeriodM15:
		return "M15"  
	case PeriodM30:
		return "M30"  
	case PeriodH1:
		return "H1"
	case PeriodH4:
		return "H4"
	case PeriodD1:
		return "D1"
	case PeriodW1:
		return "W1"
	case PeriodMN1:
		return "MN1"
	default:
		return "UNKNOWN"
	}
}
