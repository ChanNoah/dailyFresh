package controllers

import (
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/orm"
	"dailyFresh/models"
	"strconv"
	"github.com/gomodule/redigo/redis"
	"time"
	"strings"
)

type OrderController struct {
	beego.Controller
}

func(this*OrderController)HandleShowOrder(){
	//获取数据
	ids := this.GetStrings("select")
	//校验数据
	if len(ids) == 0{
		beego.Error("传输数据错误")
		return
	}
	//处理数据
	//1.获取地址信息
	userName := this.GetSession("userName")
	o := orm.NewOrm()
	//获取当前用户所有地址信息
	var adds []models.Address
	o.QueryTable("Address").RelatedSel("User").Filter("User__UserName",userName.(string)).All(&adds)
	this.Data["adds"] = adds
	//2.支付方式
	var goods []map[string]interface{}
	conn,err :=redis.Dial("tcp","192.168.110.81:6379")
	if err != nil{
		beego.Error("redis链接失败")
		return
	}
	defer conn.Close()

	//3.获取商品信息和商品数量
	totalCount := 0
	totalPrice := 0

	i := 1
	for _,id := range ids{
		skuid,_ :=strconv.Atoi(id)
		temp := make(map[string]interface{})
		//获取商品信息
		var goodsSku models.GoodsSKU
		goodsSku.Id = skuid
		o.Read(&goodsSku)

		temp["goodsSku"] = goodsSku

		//获取商品数量
		resp,err :=conn.Do("hget","cart_"+userName.(string),skuid)
		count,_ :=redis.Int(resp,err)
		temp["count"] = count
		//计算商品小计
		littlePrice := goodsSku.Price * count
		temp["littlePrice"] = littlePrice
		totalCount += 1
		totalPrice += littlePrice

		temp["i"]  = i
		i +=1

		goods = append(goods,temp)
	}

	//定义运费
	transPrice := 10
	truePrice := transPrice + totalPrice
	this.Data["totalCount"] = totalCount
	this.Data["totalPrice"] = totalPrice
	this.Data["transPrice"] = transPrice
	this.Data["truePrice"] = truePrice

	//返回数据
	this.Data["ids"] = ids
	this.Data["goods"] = goods
	this.TplName = "place_order.html"
}

//处理添加订单业务
func(this*OrderController)HandleAddOrder(){
	resp := make(map[string]interface{})
	defer AJAXRESP(&this.Controller,resp)


	//获取数据
	addrId,err1 :=this.GetInt("addId")
	skuids := this.GetString("skuids")
	payId,err2 := this.GetInt("payId")
	totalCount ,err3 := this.GetInt("totalCount")
	totalPrice,err4 := this.GetInt("totalPrice")
	transPrice,err5 := this.GetInt("transPrice")

	//校验数据u
	if err1 != nil ||  err2 != nil || err3 != nil || err4 != nil || err5 != nil{
		resp["errno"] = 1
		resp["errmsg"] = "传输数据错误"
		return
	}

	//beego.Info("addrId=",addrId,"   skuids=",skuids,"    payId=",payId,"   totalCount=",totalCount, "   totalPrice=",totalPrice,"   transPrice = ",transPrice)
	//处理数据

	//1.把获取到的数据插入到订单表
	o := orm.NewOrm()

	var orderInfo models.OrderInfo
	//插入地址信息
	var addr models.Address
	addr.Id = addrId
	o.Read(&addr)
	orderInfo.Address = &addr

	//插入用户信息
	var user models.User
	userName := this.GetSession("userName")
	user.UserName = userName.(string)
	o.Read(&user,"UserName")
	orderInfo.User = &user

	orderInfo.TransitPrice = transPrice
	orderInfo.TotalPrice = totalPrice
	orderInfo.TotalCount = totalCount
	orderInfo.PayMethod = payId
	orderInfo.OrderId = time.Now().Format("20060102150405"+strconv.Itoa(user.Id))

	//插入
	o.Begin()

	_,err := o.Insert(&orderInfo)
	if err != nil{
		resp["errno"] = 3
		resp["errmsg"] = "订单表插入失败"
	}
	//对商品Id做处理   [1   3    6    8]
	ids :=strings.Split(skuids[1:len(skuids)-1]," ")
	conn,err := redis.Dial("tcp","192.168.110.81:6379")
	if err != nil{
		resp["errno"] = 2
		resp["errmsg"] = "redis连接诶错误"
		return
	}
	defer conn.Close()

	var history_Stock int   //原有库存量

	for _,id := range ids{
		skuid,_ :=strconv.Atoi(id)
		var goodsSku models.GoodsSKU
		goodsSku.Id = skuid
		for i:=0;i<3;i++{
			o.Read(&goodsSku)
			history_Stock = goodsSku.Stock


			//获取商品数量
			re,err :=conn.Do("hget","cart_"+userName.(string),skuid)
			count,_ :=redis.Int(re,err)

			var orderGoods models.OrderGoods
			orderGoods.GoodsSKU = &goodsSku
			orderGoods.Price = count * goodsSku.Price
			orderGoods.Count = count
			orderGoods.OrderInfo = &orderInfo
			if goodsSku.Stock < count{
				resp["errno"] = 4
				resp["errmsg"] = goodsSku.Name+"库存不足"
				o.Rollback()
				return
			}
			o.Insert(&orderGoods)

			//time.Sleep(time.Second * 10)
			var goodsSku2 models.GoodsSKU
			goodsSku2.Id = goodsSku.Id
			o.Read(&goodsSku2)
			beego.Info(history_Stock,goodsSku2.Stock)
			if history_Stock != goodsSku2.Stock{
				if i == 2 {
					resp["errno"] = 6
					resp["errmsg"] = "商品数量被改变，请重新选择商品"
					o.Rollback()
					return
				}else{
					continue
				}
			}else{
				goodsSku.Stock -= count
				goodsSku.Sales += count
				_,err=o.Update(&goodsSku)
				if err!= nil{
					resp["errno"] = 7
					resp["errmsg"] = "更新错误"
					return
				}
				conn.Do("hdel","cart_"+userName.(string),skuid)
				break
			}
		}
	}

	//给容器赋值
	resp["errno"] = 5
	resp["errmsg"] = "OK"
	////把容器传递给前段
	//this.Data["json"] = resp
	////告诉前端以json格式接受
	//this.ServeJSON()
	//2.把购物车中的数据清除
	o.Commit()



	//返回数据
}
